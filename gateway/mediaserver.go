package gateway

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
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

	s3client       *s3.Client
	bucket         string
	uploadPrefix   string
	downloadPrefix string
}

var _ mediaServer = (*httpPutMediaServer)(nil)
var _ mediaServer = (*localMediaServer)(nil)
var _ mediaServer = (*s3MediaServer)(nil)

func simpleS3Config(access_key, secret_access_key string, endpoint_url string) aws.Config {
	return aws.Config{
		Region:       "custom",
		Credentials:  credentials.NewStaticCredentialsProvider(access_key, secret_access_key, ""),
		Logger:       logging.Nop{},
		BaseEndpoint: aws.String(endpoint_url),
	}
}

func createS3MediaServer(bg *config.BridgeValues, parsed *url.URL, logger *logrus.Entry) (*s3MediaServer, error) {
	optionsFromURL := parsed.Query()
	secretAccessKey, _ := parsed.User.Password()
	pathSplitted := strings.Split(strings.TrimLeft(parsed.Path, "/"), "/")

	useSSL, err := strconv.ParseBool(optionsFromURL.Get("useSSL"))
	if err != nil {
		logger.Warn("error while parsing useSSL boolean, assuming false: ", err)
		useSSL = false
	}

	pathStyle := true
	if optionsFromURL.Has("pathStyle") {
		pathStyle, err = strconv.ParseBool(optionsFromURL.Get("pathStyle"))
		if err != nil {
			logger.Warn("error while parsing pathStyle boolean, assuming false: ", err)
			pathStyle = false
		}
	}

	if len(pathSplitted) == 0 {
		return nil, fmt.Errorf("no bucket specified")
	}

	bucketName := pathSplitted[0]
	uploadPrefix := strings.Join(pathSplitted[1:], "/")

	var s3BaseUrl string
	if useSSL {
		s3BaseUrl = "https://" + parsed.Host
	} else {
		s3BaseUrl = "http://" + parsed.Host
	}

	s3cfg := simpleS3Config(parsed.User.Username(), secretAccessKey, s3BaseUrl)
	client := s3.NewFromConfig(s3cfg, func(o *s3.Options) {
		o.UsePathStyle = pathStyle
	})

	logger.WithFields(logrus.Fields{
		"bucket":       bucketName,
		"uploadPrefix": uploadPrefix,
	}).Debug("configured minio client")

	// This will return an error if the bucket does not exist
	headBucketResult, err := client.HeadBucket(context.TODO(), &s3.HeadBucketInput{Bucket: aws.String(bucketName)})
	if err != nil {
		return nil, fmt.Errorf("failed checking if bucket exists: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"bucket":           bucketName,
		"uploadPrefix":     uploadPrefix,
		"headBucketResult": headBucketResult,
	}).Debug("checked destination bucket")

	return &s3MediaServer{
		commonMediaServer: commonMediaServer{
			logger: logger,
		},

		s3client: client,

		bucket:         bucketName,
		uploadPrefix:   uploadPrefix,
		downloadPrefix: bg.General.MediaServerDownload,
	}, nil
}

func createMediaServer(bg *config.BridgeValues, logger *logrus.Entry) (mediaServer, error) {
	if bg.General.MediaServerUpload == "" && bg.General.MediaDownloadPath == "" {
		return nil, nil //  we don't have a attachfield or we don't have a mediaserver configured return
	}

	if bg.General.MediaServerUpload != "" {
		parsed, err := url.Parse(bg.General.MediaServerUpload)
		if err != nil {
			return nil, fmt.Errorf("failed parsing mediaServerUpload URL: %w", err)
		}

		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			return &httpPutMediaServer{
				commonMediaServer: commonMediaServer{
					logger: logger,
				},

				httpUploadPath:     bg.General.MediaServerUpload,
				httpDownloadPrefix: bg.General.MediaServerDownload,
			}, nil
		}

		if parsed.Scheme == "s3" {
			s3MediaServer, err := createS3MediaServer(bg, parsed, logger)
			if err == nil {
				return s3MediaServer, nil
			}

			return nil, fmt.Errorf("failed to configure s3 media server: %w", err)
		}

		return nil, fmt.Errorf("unknown schema (protocol) for mediaServerUpload: '%s'", parsed.Scheme)
	}

	if bg.General.MediaDownloadPath != "" {
		return &localMediaServer{
			commonMediaServer: commonMediaServer{
				logger: logger,
			},

			localPath:          bg.General.MediaDownloadPath,
			httpDownloadPrefix: bg.General.MediaServerDownload,
		}, nil
	}

	return nil, nil // never reached
}

// handleFilesUpload which uses MediaServerUpload configuration to upload the file via HTTP PUT request.
// Returns error on failure.
func (h *httpPutMediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	client := &http.Client{
		Timeout: time.Second * 5,
	}
	// Use MediaServerUpload. Upload using a PUT HTTP request and basicauth.
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	uploadUrl := h.httpUploadPath + "/" + sha1sum + "/" + fi.Name

	req, err := http.NewRequest(http.MethodPut, uploadUrl, bytes.NewReader(*fi.Data))
	if err != nil {
		return "", fmt.Errorf("mediaserver upload failed, could not create request: %#v", err)
	}

	h.logger.Debugf("mediaserver upload url: %s", uploadUrl)

	req.Header.Set("Content-Type", "binary/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mediaserver upload failed, could not Do request: %#v", err)
	}
	defer resp.Body.Close()

	return h.httpDownloadPrefix + "/" + sha1sum + "/" + fi.Name, nil
}

// handleFilesUpload which uses MediaServerPath configuration, places the file on the current filesystem.
// Returns error on failure.
func (h *localMediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	dir := h.localPath + "/" + sha1sum
	err := os.Mkdir(dir, os.ModePerm)
	if err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("mediaserver path failed, could not mkdir: %s %#v", err, err)
	}

	path := dir + "/" + fi.Name
	h.logger.Debugf("mediaserver path placing file: %s", path)

	err = os.WriteFile(path, *fi.Data, os.ModePerm)
	if err != nil {
		return "", fmt.Errorf("mediaserver path failed, could not writefile: %s %#v", err, err)
	}

	return h.httpDownloadPrefix + "/" + sha1sum + "/" + fi.Name, nil
}

// handleFilesUpload which uploads media to s3 compatible server.
// Returns error on failure.
func (h *s3MediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8]
	key := h.uploadPrefix + "/" + sha1sum + "/" + fi.Name
	objectSize := int64(len(*fi.Data)) // TODO: Using this, since we got this in memory anyway. Would be nicer to use fi.Size, but it is 0

	// We do not bother with multipart uploads for now, as files are expected to be small (less than 5GB).
	// If needed, we can implement that later.
	info, err := h.s3client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(h.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(*fi.Data),
		ContentLength: aws.Int64(objectSize),
		ContentType:   aws.String("application/octet-stream"),
	})
	if err != nil {
		return "", fmt.Errorf("mediaserver s3 putfile failed: %w", err)
	}

	h.logger.Debugf("successfully uploaded %v, etag: %v", key, info.ETag)
	return h.downloadPrefix + key, nil
}
