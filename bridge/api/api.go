package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/olahol/melody"
	ring "github.com/zealws/golang-ring"
)

// TODO: fork and rework golang-ring to remove all the deferred mutex unlocks

const (
	apiProtocol = "api"
	bindAddrStr = "BindAddress"
)

type API struct {
	*bridge.Config
	sync.RWMutex

	Messages  ring.Ring
	DebugMode bool
	mrouter   *melody.Melody
}

type Message struct {
	Text     string `json:"text"`
	Username string `json:"username"`
	UserID   string `json:"userid"`
	Avatar   string `json:"avatar"`
	Gateway  string `json:"gateway"`
}

func New(cfg *bridge.Config) bridge.Bridger {
	b := &API{Config: cfg}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	b.DebugMode = b.GetBool("Debug")
	b.Messages = ring.Ring{}
	b.mrouter = melody.New()

	b.mrouter.HandleMessage(func(s *melody.Session, msg []byte) {
		message := config.Message{}

		// TODO: try a faster json lib, https://github.com/goccy/go-json
		err := json.Unmarshal(msg, &message)
		if err != nil {
			b.Log.Errorf("failed to decode message from byte[] '%s'", string(msg))
			return
		}

		b.handleWebsocketMessage(message, s)
	})

	b.mrouter.HandleConnect(func(session *melody.Session) {
		greet := b.getGreeting()
		data, err := json.Marshal(greet)
		if err != nil {
			b.Log.Errorf("failed to encode message '%v'", greet)
			return
		}

		err = session.Write(data)
		if err != nil {
			b.Log.Errorf("failed to write message '%s'", string(data))
			return
		}
		// TODO: send message history buffer from `b.Messages` here
	})

	if b.GetInt("Buffer") != 0 {
		b.Messages.SetCapacity(b.GetInt("Buffer"))
	}

	if b.GetString("Token") != "" {
		e.Use(middleware.KeyAuth(func(key string, c echo.Context) (bool, error) {
			return key == b.GetString("Token"), nil
		}))
	}

	e.GET("/api/health", b.handleHealthcheck)
	e.GET("/api/messages", b.handleMessages)
	e.GET("/api/stream", b.handleStream)
	e.GET("/api/websocket", b.handleWebsocket)
	e.POST("/api/message", b.handlePostMessage)
	go func() {
		if b.GetString(bindAddrStr) == "" {
			b.Log.Fatal("No BindAddress configured.")
		}

		b.Log.Infof("Listening on %s", b.GetString(bindAddrStr))
		b.Log.Fatal(e.Start(b.GetString(bindAddrStr)))
	}()

	return b
}

func (b *API) Connect() error {
	return nil
}

func (b *API) Disconnect() error {
	return nil
}

func (b *API) JoinChannel(channel config.ChannelInfo) error {
	return nil
}

// SanitizeNick can be added to gateway/bridgemap/ files and SendMessage in gateway.go if needed
func (b *API) SanitizeNick(msg *config.Message) error {
	return nil
}

func (b *API) Send(msg config.Message) (string, error) {
	defer b.apiHandlePanic()

	// ignore delete messages
	if msg.Event == config.EventMsgDelete {
		return "", nil
	}

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	if mydebug {
		b.Log.Debugf("enqueueing message from %s on ring buffer", msg.Username)
	}

	b.Messages.Enqueue(msg)

	data, err := json.Marshal(msg)
	if err != nil {
		b.Log.Errorf("failed to encode message '%#v'", msg)
	}

	_ = b.mrouter.Broadcast(data)
	return "", nil
}

// TODO: Detect whether any locks are currently set (a feature for debug mode only)
func (b *API) apiHandlePanic() {
	rec := recover()
	if rec != nil {
		b.Log.Warnf("Recovered from panic: %#v", rec)
	}
}

func (b *API) handleHealthcheck(c echo.Context) error {
	return c.String(http.StatusOK, "OK")
}

func (b *API) handlePostMessage(c echo.Context) error {
	defer b.apiHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	message := config.Message{}
	if err := c.Bind(&message); err != nil {
		return err
	}
	// these values are fixed
	message.Channel = apiProtocol
	message.Protocol = apiProtocol
	message.Account = b.Account
	message.ID = ""
	message.Timestamp = time.Now()

	var (
		fm map[string]any
		ds string
		ok bool
	)

	for i, f := range message.Extra["file"] {
		fi := config.FileInfo{}

		if fm, ok = f.(map[string]any); !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "invalid format for extra")
		}
		err := mapstructure.Decode(fm, &fi)
		if err != nil {
			if !strings.Contains(err.Error(), "got string") {
				return err
			}
		}
		// mapstructure doesn't decode base64 into []byte, so it must be done manually for fi.Data
		if ds, ok = fm["Data"].(string); !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "invalid format for data")
		}

		data, err := base64.StdEncoding.DecodeString(ds)
		if err != nil {
			return err
		}
		fi.Data = &data
		message.Extra["file"][i] = fi
	}

	if mydebug {
		b.Log.Debugf("Sending message from %s on %s to gateway", message.Username, apiProtocol)
	}

	b.Remote <- message
	return c.JSON(http.StatusOK, message)
}

func (b *API) handleMessages(c echo.Context) error {
	defer b.apiHandlePanic()

	c.JSONPretty(http.StatusOK, b.Messages.Values(), " ")

	b.Lock()
	b.Messages = ring.Ring{}
	b.Unlock()

	return nil
}

func (b *API) getGreeting() config.Message {
	return config.Message{
		Event:     config.EventAPIConnected,
		Timestamp: time.Now(),
	}
}

func (b *API) handleStream(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusOK)
	greet := b.getGreeting()
	if err := json.NewEncoder(c.Response()).Encode(greet); err != nil {
		return err
	}

	c.Response().Flush()
	for {
		select {
		// TODO: this causes issues, messages should be broadcasted to all connected clients
		default:
			msg := b.Messages.Dequeue()
			if msg != nil {
				if err := json.NewEncoder(c.Response()).Encode(msg); err != nil {
					return err
				}
				c.Response().Flush()
			}
			time.Sleep(100 * time.Millisecond)
		case <-c.Request().Context().Done():
			return nil
		}
	}
}

func (b *API) handleWebsocketMessage(message config.Message, s *melody.Session) {
	defer b.apiHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	message.Channel = apiProtocol
	message.Protocol = apiProtocol
	message.Account = b.Account
	// TODO: msg ID's?
	message.ID = ""
	message.Timestamp = time.Now()

	// todo: reuse this buffer
	data, err := json.Marshal(message)
	if err != nil {
		b.Log.Errorf("failed to encode message for loopback '%v'", message)
		return
	}
	_ = b.mrouter.BroadcastOthers(data, s)

	if mydebug {
		b.Log.Debugf("=> [api] Sending websocket message from %s to gateway", message.Username)
	}

	b.Remote <- message
}

func (b *API) handleWebsocket(c echo.Context) error {
	err := b.mrouter.HandleRequest(c.Response(), c.Request())
	if err != nil {
		b.Log.Errorf("error in websocket handling: %s", err)
		return err
	}

	return nil
}
