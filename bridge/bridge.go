package bridge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/sirupsen/logrus"
)

type Bridger interface {
	Send(msg config.Message) (string, error)
	Connect() error
	JoinChannel(channel config.ChannelInfo) error
	Disconnect() error
	NewHttpRequest(method, uri string, body io.Reader) (*http.Request, error)
	NewHttpClient(proxy string) (*http.Client, error)
}

type Bridge struct {
	Bridger
	*sync.RWMutex

	Name           string
	Account        string
	Protocol       string
	Channels       map[string]config.ChannelInfo
	Joined         map[string]bool
	ChannelMembers *config.ChannelMembers
	Log            *logrus.Entry
	Config         config.Config
	General        *config.Protocol
	HttpClient     *http.Client // Unique HTTP settings per bridge
}

type Config struct {
	*Bridge

	Remote chan config.Message
}

// Factory is the factory function to create a bridge
type Factory func(*Config) Bridger

// New is a basic constructor. More important fields are populated
// in gateway/gateway.go (AddBridge method).
func New(bridge *config.Bridge) *Bridge {
	accInfo := strings.Split(bridge.Account, ".")
	if len(accInfo) != 2 {
		log.Fatalf("config failure, account incorrect: %s", bridge.Account)
	}

	protocol := accInfo[0]
	name := accInfo[1]

	return &Bridge{
		RWMutex:  new(sync.RWMutex),
		Channels: make(map[string]config.ChannelInfo),
		Name:     name,
		Protocol: protocol,
		Account:  bridge.Account,
		Joined:   make(map[string]bool),
	}
}

func (b *Bridge) JoinChannels() error {
	return b.joinChannels(b.Channels, b.Joined)
}

// SetChannelMembers sets the newMembers to the bridge ChannelMembers
func (b *Bridge) SetChannelMembers(newMembers *config.ChannelMembers) {
	b.Lock()
	b.ChannelMembers = newMembers
	b.Unlock()
}

func (b *Bridge) joinChannels(channels map[string]config.ChannelInfo, exists map[string]bool) error {
	for ID, channel := range channels {
		if !exists[ID] {
			b.Log.Infof("%s: joining %s (ID: %s)", b.Account, channel.Name, ID)
			time.Sleep(time.Duration(b.GetInt("JoinDelay")) * time.Millisecond)
			err := b.JoinChannel(channel)
			if err != nil {
				return err
			}
			exists[ID] = true
		}
	}
	return nil
}

func (b *Bridge) GetConfigKey(key string) string {
	return b.Account + "." + key
}

func (b *Bridge) IsKeySet(key string) bool {
	return b.Config.IsKeySet(b.GetConfigKey(key)) || b.Config.IsKeySet("general."+key)
}

func (b *Bridge) GetBool(key string) bool {
	val, ok := b.Config.GetBool(b.GetConfigKey(key))
	if !ok {
		val, _ = b.Config.GetBool("general." + key)
	}
	return val
}

func (b *Bridge) GetInt(key string) int {
	val, ok := b.Config.GetInt(b.GetConfigKey(key))
	if !ok {
		val, _ = b.Config.GetInt("general." + key)
	}
	return val
}

func (b *Bridge) GetString(key string) string {
	val, ok := b.Config.GetString(b.GetConfigKey(key))
	if !ok {
		val, _ = b.Config.GetString("general." + key)
	}
	return val
}

func (b *Bridge) GetStringSlice(key string) []string {
	val, ok := b.Config.GetStringSlice(b.GetConfigKey(key))
	if !ok {
		val, _ = b.Config.GetStringSlice("general." + key)
	}
	return val
}

func (b *Bridge) GetStringSlice2D(key string) [][]string {
	val, ok := b.Config.GetStringSlice2D(b.GetConfigKey(key))
	if !ok {
		val, _ = b.Config.GetStringSlice2D("general." + key)
	}
	return val
}

// NewHttpClient produces a single unified http.Client per bridge.
//
// This allows to have project-wide defaults (timeout) as well as
// bridge-configurable values (`http_proxy`).
//
// This method is left public so that if that's needed, a bridge can
// override this constructor.
//
// TODO: maybe protocols without HTTP downloads at all could override
// this method and return nil? Or the other way around?
func (b *Bridge) NewHttpClient(http_proxy string) (*http.Client, error) {
	if http_proxy != "" {
		proxyUrl, err := url.Parse(b.GetString("http_proxy"))
		if err != nil {
			return nil, err
		}

		b.Log.Debugf("%s using HTTP proxy %s", b.Protocol, proxyUrl)

		return &http.Client{
			Timeout:   time.Second * 15,
			Transport: &http.Transport{Proxy: http.ProxyURL(proxyUrl)},
		}, nil
	}

	b.Log.Debugf("%s not using HTTP proxy", b.Protocol)

	return &http.Client{
		Timeout: time.Second * 5,
	}, nil
}

var errHttpGetNotOk = errors.New("HTTP server responded non-OK code")

func HttpGetNotOkError(uri string, code int) error {
	return fmt.Errorf("%w: %s returned code %d", errHttpGetNotOk, uri, code)
}

func (b *Bridge) HttpGetBytes(uri string) (*[]byte, error) {
	req, err := b.Bridger.NewHttpRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	b.Log.Debugf("Getting HTTP bytes with request: %#v", req)

	resp, err := b.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, HttpGetNotOkError(uri, resp.StatusCode)
	}

	var buf bytes.Buffer

	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		return nil, err
	}

	err = resp.Body.Close()
	if err != nil {
		return nil, err
	}

	data := buf.Bytes()

	return &data, nil
}

func (b *Bridge) AddAttachmentFromURL(msg *config.Message, filename string, id string, comment string, uri string) error {
	return b.addAttachment(msg, filename, id, comment, uri, nil, false)
}

func (b *Bridge) AddAttachmentFromBytes(msg *config.Message, filename string, id string, comment string, data *[]byte) error {
	return b.addAttachment(msg, filename, id, comment, "", data, false)
}

func (b *Bridge) AddAvatarFromURL(msg *config.Message, filename string, id string, comment string, uri string) error {
	return b.addAttachment(msg, filename, id, comment, uri, nil, true)
}

func (b *Bridge) AddAvatarFromBytes(msg *config.Message, filename string, id string, comment string, data *[]byte) error {
	return b.addAttachment(msg, filename, id, comment, "", data, true)
}

// NewHttpRequest produces a new http.Request instance with bridge-specific settings.
//
// This is used by bridges where HTTP downloads require a cookie/token, by overriding
// this method in the bridge struct.
func (b *Bridge) NewHttpRequest(method, uri string, body io.Reader) (*http.Request, error) {
	return http.NewRequest(method, uri, body)
}

// Internal method including common parts to attachment/avatar handling methods.
//
// This method will process received bytes. If bytes are not set, they will be downloaded from the given URL.
// If neither data bytes nor uri is provided, this will be a hard error because there's a logic error somewhere.
func (b *Bridge) addAttachment(msg *config.Message, filename string, id string, comment string, uri string, data *[]byte, avatar bool) error {
	if data != nil {
		return b.addAttachmentProcess(msg, filename, id, comment, uri, data, avatar)
	}

	if uri == "" {
		// This should never happen
		b.Log.Fatalf("Logic error in bridge %s: attachment should have either URL or data set, neither was provided", b.Protocol)
	}

	data, err := b.HttpGetBytes(uri)
	if err != nil {
		return err
	}

	return b.addAttachmentProcess(msg, filename, id, comment, uri, data, avatar)
}

type errFileTooLarge struct {
	FileName string
	Size     int
	MaxSize  int
}

func (e *errFileTooLarge) Error() string {
	return fmt.Sprintf("File %#v to large to download (%#v). MediaDownloadSize is %#v", e.FileName, e.Size, e.MaxSize)
}

type errFileBlacklisted struct {
	FileName string
}

func (e *errFileBlacklisted) Error() string {
	return fmt.Sprintf("File %#v matches the backlist, not downloading it", e.FileName)
}

func (b *Bridge) addAttachmentProcess(msg *config.Message, filename string, id string, comment string, uri string, data *[]byte, avatar bool) error {
	size := len(*data)
	if size > b.General.MediaDownloadSize {
		return &errFileTooLarge{
			FileName: filename,
			Size:     size,
			MaxSize:  b.General.MediaDownloadSize,
		}
	}

	// Apply `MediaDownloadBlackList` regexes
	if b.Config.IsFilenameBlacklisted(filename) {
		return &errFileBlacklisted{
			FileName: filename,
		}
	}

	b.Log.Debugf("Download OK %#v %#v", filename, size)
	msg.Extra["file"] = append(msg.Extra["file"], config.FileInfo{
		Name:    filename,
		Data:    data,
		URL:     uri,
		Comment: comment,
		Avatar:  avatar,
		// TODO: if id is not set, maybe use hash of bytes?
		NativeID: id,
	})

	return nil
}
