package gateway

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/logging"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/sirupsen/logrus"
)

type mediaServer interface {
	handleFilesUpload(fi *config.FileInfo) (string, error)
}

type commonMediaServer struct {
	logger *logrus.Entry
}

type httpPutMediaServer struct {
	commonMediaServer

	httpUploadPath     string
	httpDownloadPrefix string
}

type localMediaServer struct {
	commonMediaServer

	localPath          string
	httpDownloadPrefix string
}

type s3MediaServer struct {
	commonMediaServer

	s3Client        *s3.Client
	presignS3Client *s3.PresignClient

	bucket             string
	uploadPrefix       string
	httpDownloadPrefix string
}

var _ mediaServer = (*httpPutMediaServer)(nil)
var _ mediaServer = (*localMediaServer)(nil)
var _ mediaServer = (*s3MediaServer)(nil)

const mediaUploadTimeout = 5 * time.Second
const mediaUploadPresignDuration = 7 * 24 * time.Hour // presigned URL valid duration

var ErrMediaConfiguration = errors.New("media server is not properly configured")
var ErrMediaConfigurationNotWanted = errors.New("media server is not configured and not wanted")

var ErrMediaServerRuntime = errors.New("media server error")
var errUploadFailed = fmt.Errorf("%w: upload failed", ErrMediaServerRuntime)

func createS3MediaServer(bg *config.BridgeValues, bucketName string, uploadPrefix string, logger *logrus.Entry) (*s3MediaServer, error) {
	if bucketName == "" {
		return nil, fmt.Errorf("%w: invalid s3 upload prefix, must be in format s3://bucketname/prefix", ErrMediaConfiguration)
	}

	if bg.General.S3Endpoint == "" {
		return nil, fmt.Errorf("%w: s3 endpoint is not configured", ErrMediaConfiguration)
	}

	if bg.General.S3AccessKey == "" {
		return nil, fmt.Errorf("%w: s3 access key is not configured", ErrMediaConfiguration)
	}

	if bg.General.S3SecretKey == "" {
		return nil, fmt.Errorf("%w: s3 secret key is not configured", ErrMediaConfiguration)
	}

	uploadPrefix = strings.Trim(uploadPrefix, "/")

	client := s3.NewFromConfig(aws.Config{
		Region:       "custom",
		Credentials:  credentials.NewStaticCredentialsProvider(bg.General.S3AccessKey, bg.General.S3SecretKey, ""),
		Logger:       logging.Nop{},
		BaseEndpoint: aws.String(bg.General.S3Endpoint),
		HTTPClient:   &http.Client{Timeout: mediaUploadTimeout},
	}, func(o *s3.Options) {
		o.UsePathStyle = bg.General.S3ForcePathStyle
	})

	var presignClient *s3.PresignClient
	if bg.General.S3Presign {
		presignClient = s3.NewPresignClient(client)
	}

	// This will return an error if the bucket does not exist
	headBucketResult, err := client.HeadBucket(context.TODO(), &s3.HeadBucketInput{Bucket: aws.String(bucketName)})
	if err != nil {
		return nil, fmt.Errorf("%w: failed to check if bucket exists: %w", ErrMediaServerRuntime, err)
	}

	logger.WithFields(logrus.Fields{
		"bucket":           bucketName,
		"uploadPrefix":     uploadPrefix,
		"baseUrl":          bg.General.S3Endpoint,
		"pathStyle":        bg.General.S3ForcePathStyle,
		"headBucketResult": headBucketResult,
	}).Debug("checked destination bucket")

	return &s3MediaServer{
		commonMediaServer: commonMediaServer{
			logger: logger,
		},

		s3Client:        client,
		presignS3Client: presignClient,

		bucket:             bucketName,
		uploadPrefix:       uploadPrefix,
		httpDownloadPrefix: bg.General.MediaServerDownload,
	}, nil
}

func createMediaServer(bg *config.BridgeValues, logger *logrus.Entry) (mediaServer, error) {
	if bg.General.MediaServerUpload == "" && bg.General.MediaDownloadPath == "" {
		return nil, ErrMediaConfigurationNotWanted //  we don't have a attachfield or we don't have a mediaserver configured return
	}

	if bg.General.MediaServerUpload != "" {
		parsed, err := url.Parse(bg.General.MediaServerUpload)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid media server upload URL: %w", ErrMediaConfiguration, err)
		}

		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			return &httpPutMediaServer{
				commonMediaServer: commonMediaServer{
					logger: logger.WithField("component", "httpputmediaserver"),
				},

				httpUploadPath:     bg.General.MediaServerUpload,
				httpDownloadPrefix: bg.General.MediaServerDownload,
			}, nil
		}

		if parsed.Scheme == "s3" {
			s3MediaServer, err := createS3MediaServer(bg, parsed.Host, parsed.Path, logger.WithField("component", "s3mediaserver"))
			if err == nil {
				return s3MediaServer, nil
			}

			return nil, fmt.Errorf("%w: %w", ErrMediaConfiguration, err)
		}

		return nil, fmt.Errorf("%w: unknown schema (protocol) for mediaServerUpload: '%s'", ErrMediaConfiguration, parsed.Scheme)
	}

	if bg.General.MediaDownloadPath != "" {
		return &localMediaServer{
			commonMediaServer: commonMediaServer{
				logger: logger.WithField("component", "localmediaserver"),
			},

			localPath:          bg.General.MediaDownloadPath,
			httpDownloadPrefix: bg.General.MediaServerDownload,
		}, nil
	}

	return nil, ErrMediaConfigurationNotWanted // never reached
}

// handleFilesUpload which uses MediaServerUpload configuration to upload the file via HTTP PUT request.
// Returns error on failure.
func (h *httpPutMediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	client := &http.Client{
		Timeout: mediaUploadTimeout,
	}
	// Use MediaServerUpload. Upload using a PUT HTTP request and basicauth.
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	uploadUrl := h.httpUploadPath + "/" + path.Join(sha1sum, fi.Name)

	req, err := http.NewRequest(http.MethodPut, uploadUrl, bytes.NewReader(*fi.Data))
	if err != nil {
		return "", fmt.Errorf("%w: could not create request: %w", errUploadFailed, err)
	}

	h.logger.Debugf("mediaserver upload url: %s", uploadUrl)

	req.Header.Set("Content-Type", "binary/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: could not Do request: %w", errUploadFailed, err)
	}

	err = resp.Body.Close()
	if err != nil {
		h.logger.WithError(err).Error("failed to close response body")
	}

	return h.httpDownloadPrefix + "/" + path.Join(sha1sum, fi.Name), nil
}

// handleFilesUpload which uses MediaServerPath configuration, places the file on the current filesystem.
// Returns error on failure.
func (h *localMediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	dir := path.Join(h.localPath, sha1sum)

	err := os.Mkdir(dir, 0755) //nolint:gosec // this is for writing media files, so 0755 is fine, we want them to be accesible by webserver
	if err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("%w: could not mkdir: %w", errUploadFailed, err)
	}

	fileWritePath := path.Join(dir, fi.Name)
	h.logger.WithField("fileWritePath", fileWritePath).Debug("mediaserver path placing file")

	err = os.WriteFile(fileWritePath, *fi.Data, 0644) //nolint:gosec // this is for writing media files, so 0644 is fine, we want them to be accesible by webserver
	if err != nil {
		return "", fmt.Errorf("%w: could not writefile: %w", errUploadFailed, err)
	}

	return h.httpDownloadPrefix + "/" + path.Join(sha1sum, fi.Name), nil
}

// handleFilesUpload which uploads media to s3 compatible server.
// Returns error on failure.
func (h *s3MediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	key := path.Join(h.uploadPrefix, sha1sum, fi.Name)
	objectSize := int64(len(*fi.Data)) // TODO: Using this, sine we got this in memory anyway. Would be nicer to use fi.Size, but it is 0

	// We do not bother with multipart uploads for now, as files are expected to be small (less than 5GB).
	// If needed, we can implement that later.
	info, err := h.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(h.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(*fi.Data),
		ContentLength: aws.Int64(objectSize),
		ContentType:   aws.String("application/octet-stream"),
	})
	if err != nil {
		return "", fmt.Errorf("%w: mediaserver s3 PutObject failed: %w", errUploadFailed, err)
	}

	downloadURL := h.httpDownloadPrefix + "/" + key
	// If presign is enabled, generate a presigned URL, otherwise use the standard download URL.
	if h.presignS3Client != nil {
		downloadReq, err := h.presignS3Client.PresignGetObject(context.TODO(), &s3.GetObjectInput{
			Bucket: aws.String(h.bucket),
			Key:    aws.String(key),
		}, s3.WithPresignExpires(mediaUploadPresignDuration))
		if err != nil {
			return "", fmt.Errorf("%w: mediaserver s3 presign request creation failed: %w", errUploadFailed, err)
		}

		downloadURL = downloadReq.URL
	}

	h.logger.WithFields(logrus.Fields{
		"key":         key,
		"etag":        info.ETag,
		"downloadURL": downloadURL,
	}).Debug("successfully uploaded")

	return downloadURL, nil
}
