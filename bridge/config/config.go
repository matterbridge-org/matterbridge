package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const (
	EventJoinLeave         = "join_leave"
	EventTopicChange       = "topic_change"
	EventFailure           = "failure"
	EventFileFailureSize   = "file_failure_size"
	EventAvatarDownload    = "avatar_download"
	EventRejoinChannels    = "rejoin_channels"
	EventUserAction        = "user_action"
	EventMsgDelete         = "msg_delete"
	EventFileDelete        = "file_delete"
	EventAPIConnected      = "api_connected"
	EventUserTyping        = "user_typing"
	EventGetChannelMembers = "get_channel_members"
	EventNoticeIRC         = "notice_irc"
)

const ParentIDNotFound = "msg-parent-not-found"

type Message struct {
	Text      string    `json:"text"`
	Channel   string    `json:"channel"`
	Username  string    `json:"username"`
	UserID    string    `json:"userid"` // userid on the bridge
	Avatar    string    `json:"avatar"`
	Account   string    `json:"account"`
	Event     string    `json:"event"`
	Protocol  string    `json:"protocol"`
	Gateway   string    `json:"gateway"`
	ParentID  string    `json:"parent_id"`
	Timestamp time.Time `json:"timestamp"`
	ID        string    `json:"id"`
	Extra     map[string][]interface{}
}

func (m Message) ParentNotFound() bool {
	return m.ParentID == ParentIDNotFound
}

func (m Message) ParentValid() bool {
	return m.ParentID != "" && !m.ParentNotFound()
}

// GetFileInfos extracts typed FileInfo list from the message.
//
// This method is guaranteed not to fail. The inner type casting should never
// fail, but will simply produce a warning if that's the case.
func (m Message) GetFileInfos(log *logrus.Entry) *[]*FileInfo {
	var fileInfos []*FileInfo

	for _, file := range m.Extra["file"] {
		fileInfo, ok := file.(FileInfo)
		if !ok {
			// This should never happen, unless a bridge receiving an external message
			// produces an invalid Extra field where the File is not valid FileInfo.
			// TODO: log more information about the message for debugging.
			log.Warn(FileCastError())
			continue
		}

		fileInfos = append(fileInfos, &fileInfo)
	}

	return &fileInfos
}

// FileInfo is an attachment contained in a message.
//
// When receiving an attachment (eg. an image), a bridge should populate the
// Data/Size fields.
//
// When the media server is enabled, for services that don't support file upload
// (such as IRC), the gateway router will upload the file to the media server
// and populate the URL/SHA fields. The Data/Size fields are not removed
// in this process. See handleFiles in gateway/handlers.go
type FileInfo struct {
	Name     string
	Data     *[]byte
	Comment  string
	URL      string
	Size     int64
	Avatar   bool
	SHA      string
	NativeID string
}

var errFileCast = errors.New("failed to cast config.FileInfo")

func FileCastError() error {
	return fmt.Errorf("%w", errFileCast)
}

type ChannelInfo struct {
	Name        string
	Account     string
	Direction   string
	ID          string
	SameChannel map[string]bool
	Options     ChannelOptions
}

type ChannelMember struct {
	Username    string
	Nick        string
	UserID      string
	ChannelID   string
	ChannelName string
}

type ChannelMembers []ChannelMember

type Protocol struct {
	AllowMention           []string // discord
	BindAddress            string   // mattermost, slack // DEPRECATED
	Buffer                 int      // api
	Charset                string   // irc
	ClientID               string   // msteams
	ColorNicks             bool     // only irc for now
	Debug                  bool     // general
	DebugLevel             int      // only for irc now
	DisableWebPagePreview  bool     // telegram
	EditSuffix             string   // mattermost, slack, discord, telegram
	EditDisable            bool     // mattermost, slack, discord, telegram
	HTMLDisable            bool     // matrix
	IconURL                string   // mattermost, slack
	IgnoreFailureOnStart   bool     // general
	IgnoreNicks            string   // all protocols
	IgnoreMessages         string   // all protocols
	Jid                    string   // xmpp
	JoinDelay              string   // all protocols
	Label                  string   // all protocols
	Login                  string   // mattermost, matrix
	LogFile                string   // general
	MediaDownloadBlackList []string
	MediaDownloadPath      string // Basically MediaServerUpload, but instead of uploading it, just write it to a file on the same server.
	MediaDownloadSize      int    // all protocols
	MediaServerDownload    string
	MediaServerUpload      string
	MediaConvertTgs        string     // telegram
	MediaConvertWebPToPNG  bool       // telegram
	MessageDelay           int        // IRC, time in millisecond to wait between messages
	MessageFormat          string     // telegram
	MessageLength          int        // IRC, max length of a message allowed
	MessageQueue           int        // IRC, size of message queue for flood control
	MessageSplit           bool       // IRC, split long messages with newlines on MessageLength instead of clipping
	MessageSplitMaxCount   int        // discord, split long messages into at most this many messages instead of clipping (MessageLength=1950 cannot be configured)
	Muc                    string     // xmpp
	MxID                   string     // matrix
	Name                   string     // all protocols
	Nick                   string     // all protocols
	NickFormatter          string     // mattermost, slack
	NickServNick           string     // IRC
	NickServUsername       string     // IRC
	NickServPassword       string     // IRC
	NicksPerRow            int        // mattermost, slack
	NoHomeServerSuffix     bool       // matrix
	NoSendJoinPart         bool       // all protocols
	NoTLS                  bool       // mattermost, xmpp
	Password               string     // IRC,mattermost,XMPP,matrix
	PrefixMessagesWithNick bool       // mattemost, slack
	PreserveThreading      bool       // slack
	Protocol               string     // all protocols
	QuoteDisable           bool       // telegram
	QuoteFormat            string     // telegram
	QuoteLengthLimit       int        // telegram
	RealName               string     // IRC
	RejoinDelay            int        // IRC
	ReplaceMessages        [][]string // all protocols
	ReplaceNicks           [][]string // all protocols
	RemoteNickFormat       string     // all protocols
	RunCommands            []string   // IRC
	Server                 string     // IRC,mattermost,XMPP,discord,matrix
	SessionFile            string     // msteams,whatsapp
	ShowJoinPart           bool       // all protocols
	ShowTopicChange        bool       // slack
	ShowUserTyping         bool       // slack
	ShowEmbeds             bool       // discord
	SkipTLSVerify          bool       // IRC, mattermost
	SkipVersionCheck       bool       // mattermost
	StripNick              bool       // all protocols
	StripMarkdown          bool       // irc
	SyncTopic              bool       // slack
	TengoModifyMessage     string     // general
	Team                   string     // mattermost
	TeamID                 string     // msteams
	TenantID               string     // msteams
	Token                  string     // slack, discord, api, matrix
	Topic                  string     // zulip
	URL                    string     // mattermost, slack // DEPRECATED
	UseAPI                 bool       // mattermost, slack
	UseLocalAvatar         []string   // discord
	UseSASL                bool       // IRC
	UseTLS                 bool       // IRC
	UseDiscriminator       bool       // discord
	UseFirstName           bool       // telegram
	UseUserName            bool       // discord, matrix, mattermost
	UseInsecureURL         bool       // telegram
	UserName               string     // IRC
	VerboseJoinPart        bool       // IRC
	WebhookBindAddress     string     // mattermost, slack
	WebhookURL             string     // mattermost, slack
}

type ChannelOptions struct {
	Key        string // irc, xmpp
	WebhookURL string // discord
	Topic      string // zulip
}

type Bridge struct {
	Account     string
	Channel     string
	Options     ChannelOptions
	SameChannel bool
}

type Gateway struct {
	Name   string
	Enable bool
	In     []Bridge
	Out    []Bridge
	InOut  []Bridge
}

type Tengo struct {
	InMessage        string
	Message          string
	RemoteNickFormat string
	OutMessage       string
}

type SameChannelGateway struct {
	Name     string
	Enable   bool
	Channels []string
	Accounts []string
}

type BridgeValues struct {
	API                map[string]Protocol
	IRC                map[string]Protocol
	Mattermost         map[string]Protocol
	Matrix             map[string]Protocol
	Slack              map[string]Protocol
	SlackLegacy        map[string]Protocol
	Steam              map[string]Protocol
	XMPP               map[string]Protocol
	Discord            map[string]Protocol
	Telegram           map[string]Protocol
	Rocketchat         map[string]Protocol
	SSHChat            map[string]Protocol
	WhatsApp           map[string]Protocol // TODO is this struct used? Search for "SlackLegacy" for example didn't return any results
	Zulip              map[string]Protocol
	Keybase            map[string]Protocol
	Mumble             map[string]Protocol
	General            Protocol
	Tengo              Tengo
	Gateway            []Gateway
	SameChannelGateway []SameChannelGateway
}

type Config interface {
	Viper() *viper.Viper
	BridgeValues() *BridgeValues
	IsKeySet(key string) bool
	GetBool(key string) (bool, bool)
	GetInt(key string) (int, bool)
	GetString(key string) (string, bool)
	GetStringSlice(key string) ([]string, bool)
	GetStringSlice2D(key string) ([][]string, bool)
}

type config struct {
	sync.RWMutex

	logger *logrus.Entry
	v      *viper.Viper
	cv     *BridgeValues
}

// NewConfig instantiates a new configuration based on the specified configuration file path.
func NewConfig(rootLogger *logrus.Logger, cfgfile string) Config {
	logger := rootLogger.WithFields(logrus.Fields{"prefix": "config"})

	viper.SetConfigFile(cfgfile)
	input, err := ioutil.ReadFile(cfgfile)
	if err != nil {
		logger.Fatalf("Failed to read configuration file: %#v", err)
	}

	cfgtype := detectConfigType(cfgfile)
	mycfg := newConfigFromString(logger, input, cfgtype)
	// TODO: log file should probably also be reloaded
	if mycfg.cv.General.LogFile != "" {
		logfile, err := os.OpenFile(mycfg.cv.General.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err == nil {
			logger.Info("Opening log file ", mycfg.cv.General.LogFile)
			rootLogger.Out = logfile
		} else {
			logger.Warn("Failed to open ", mycfg.cv.General.LogFile)
		}
	}
	if mycfg.cv.General.MediaDownloadSize == 0 {
		mycfg.cv.General.MediaDownloadSize = 1000000
	}
	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		logger.Println("Config file changed:", e.Name)
		// Reload BridgeValues as exposed in the Router
		// TODO: are we breaking some stuff that should *not* be reloaded here? Should
		// we only reload General section?
		//
		// This is where syntax errors are caught
		err = viper.ReadInConfig()
		if err != nil {
			logger.Errorf("%v", err)
		}

		err = viper.Unmarshal(&mycfg.cv)
		if err != nil {
			logger.Fatalf("This should not happen when reloading config: %v", err)
		}
	})
	return mycfg
}

// detectConfigType detects JSON and YAML formats, defaults to TOML.
func detectConfigType(cfgfile string) string {
	fileExt := filepath.Ext(cfgfile)
	switch fileExt {
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	}
	return "toml"
}

// NewConfigFromString instantiates a new configuration based on the specified string.
func NewConfigFromString(rootLogger *logrus.Logger, input []byte) Config {
	logger := rootLogger.WithFields(logrus.Fields{"prefix": "config"})
	return newConfigFromString(logger, input, "toml")
}

func newConfigFromString(logger *logrus.Entry, input []byte, cfgtype string) *config {
	viper.SetConfigType(cfgtype)
	viper.SetEnvPrefix("matterbridge")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadConfig(bytes.NewBuffer(input)); err != nil {
		logger.Fatalf("Failed to parse the configuration: %s", err)
	}

	cfg := &BridgeValues{}
	if err := viper.Unmarshal(cfg); err != nil {
		logger.Fatalf("Failed to load the configuration: %s", err)
	}
	return &config{
		logger: logger,
		v:      viper.GetViper(),
		cv:     cfg,
	}
}

func (c *config) BridgeValues() *BridgeValues {
	return c.cv
}

func (c *config) Viper() *viper.Viper {
	return c.v
}

func (c *config) IsKeySet(key string) bool {
	c.RLock()
	defer c.RUnlock()
	return c.v.IsSet(key)
}

func (c *config) GetBool(key string) (bool, bool) {
	c.RLock()
	defer c.RUnlock()
	return c.v.GetBool(key), c.v.IsSet(key)
}

func (c *config) GetInt(key string) (int, bool) {
	c.RLock()
	defer c.RUnlock()
	return c.v.GetInt(key), c.v.IsSet(key)
}

func (c *config) GetString(key string) (string, bool) {
	c.RLock()
	defer c.RUnlock()
	return c.v.GetString(key), c.v.IsSet(key)
}

func (c *config) GetStringSlice(key string) ([]string, bool) {
	c.RLock()
	defer c.RUnlock()
	return c.v.GetStringSlice(key), c.v.IsSet(key)
}

func (c *config) GetStringSlice2D(key string) ([][]string, bool) {
	c.RLock()
	defer c.RUnlock()

	res, ok := c.v.Get(key).([]interface{})
	if !ok {
		return nil, false
	}
	var result [][]string
	for _, entry := range res {
		result2 := []string{}
		for _, entry2 := range entry.([]interface{}) {
			result2 = append(result2, entry2.(string))
		}
		result = append(result, result2)
	}
	return result, true
}

func GetIconURL(msg *Message, iconURL string) string {
	info := strings.Split(msg.Account, ".")
	protocol := info[0]
	name := info[1]
	iconURL = strings.ReplaceAll(iconURL, "{NICK}", msg.Username)
	iconURL = strings.ReplaceAll(iconURL, "{BRIDGE}", name)
	iconURL = strings.ReplaceAll(iconURL, "{PROTOCOL}", protocol)
	return iconURL
}

type TestConfig struct {
	Config

	Overrides map[string]interface{}
}

func (c *TestConfig) IsKeySet(key string) bool {
	_, ok := c.Overrides[key]
	return ok || c.Config.IsKeySet(key)
}

func (c *TestConfig) GetBool(key string) (bool, bool) {
	val, ok := c.Overrides[key]
	if ok {
		return val.(bool), true
	}
	return c.Config.GetBool(key)
}

func (c *TestConfig) GetInt(key string) (int, bool) {
	if val, ok := c.Overrides[key]; ok {
		return val.(int), true
	}
	return c.Config.GetInt(key)
}

func (c *TestConfig) GetString(key string) (string, bool) {
	if val, ok := c.Overrides[key]; ok {
		return val.(string), true
	}
	return c.Config.GetString(key)
}

func (c *TestConfig) GetStringSlice(key string) ([]string, bool) {
	if val, ok := c.Overrides[key]; ok {
		return val.([]string), true
	}
	return c.Config.GetStringSlice(key)
}

func (c *TestConfig) GetStringSlice2D(key string) ([][]string, bool) {
	if val, ok := c.Overrides[key]; ok {
		return val.([][]string), true
	}
	return c.Config.GetStringSlice2D(key)
}
