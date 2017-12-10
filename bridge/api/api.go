package api

import (
	"encoding/json"
	"github.com/42wim/matterbridge/bridge/config"
	log "github.com/Sirupsen/logrus"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/zfjagann/golang-ring"
	"net/http"
	"sync"
	"time"
)

type Api struct {
	Config   *config.Protocol
	Remote   chan config.Message
	Account  string
	Messages ring.Ring
	sync.RWMutex
}

type ApiMessage struct {
	Text     string `json:"text"`
	Username string `json:"username"`
	UserID   string `json:"userid"`
	Avatar   string `json:"avatar"`
	Gateway  string `json:"gateway"`
}

var flog *log.Entry
var protocol = "api"

func init() {
	flog = log.WithFields(log.Fields{"module": protocol})
}

func New(cfg config.Protocol, account string, c chan config.Message) *Api {
	b := &Api{}
	e := echo.New()
	b.Messages = ring.Ring{}
	b.Messages.SetCapacity(cfg.Buffer)
	b.Config = &cfg
	b.Account = account
	b.Remote = c
	if b.Config.Token != "" {
		e.Use(middleware.KeyAuth(func(key string, c echo.Context) (bool, error) {
			return key == b.Config.Token, nil
		}))
	}
	e.GET("/api/messages", b.handleMessages)
	e.GET("/api/stream", b.handleStream)
	e.POST("/api/message", b.handlePostMessage)
	go func() {
		flog.Fatal(e.Start(cfg.BindAddress))
	}()
	return b
}

func (b *Api) Connect() error {
	return nil
}
func (b *Api) Disconnect() error {
	return nil

}
func (b *Api) JoinChannel(channel config.ChannelInfo) error {
	return nil

}

func (b *Api) Send(msg config.Message) (string, error) {
	b.Lock()
	defer b.Unlock()
	// ignore delete messages
	if msg.Event == config.EVENT_MSG_DELETE {
		return "", nil
	}
	b.Messages.Enqueue(&msg)
	return "", nil
}

func (b *Api) handlePostMessage(c echo.Context) error {
	message := &ApiMessage{}
	if err := c.Bind(message); err != nil {
		return err
	}
	flog.Debugf("Sending message from %s on %s to gateway", message.Username, "api")
	b.Remote <- config.Message{
		Text:     message.Text,
		Username: message.Username,
		UserID:   message.UserID,
		Channel:  "api",
		Avatar:   message.Avatar,
		Account:  b.Account,
		Gateway:  message.Gateway,
		Protocol: "api",
	}
	return c.JSON(http.StatusOK, message)
}

func (b *Api) handleMessages(c echo.Context) error {
	b.Lock()
	defer b.Unlock()
	c.JSONPretty(http.StatusOK, b.Messages.Values(), " ")
	b.Messages = ring.Ring{}
	return nil
}

func (b *Api) handleStream(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusOK)
	closeNotifier := c.Response().CloseNotify()
	for {
		select {
		case <-closeNotifier:
			return nil
		default:
			msg := b.Messages.Dequeue()
			if msg != nil {
				if err := json.NewEncoder(c.Response()).Encode(msg); err != nil {
					return err
				}
				c.Response().Flush()
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
