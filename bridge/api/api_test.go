package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/olahol/melody"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	apiChannel     = "api"
	channel2       = "channel2"
	channelOmitted = ""
)

func TestHandlePostMessageChannel(t *testing.T) {
	tests := []struct {
		channelJSON string
		text        string
		wantChannel string
	}{
		{channelJSON: channelOmitted, text: "http-channel omitted", wantChannel: apiChannel},
		{channelJSON: `,"channel":"` + channel2 + `"`, text: "http-channel2", wantChannel: channel2},
		{channelJSON: `,"channel":"` + apiChannel + `"`, text: "http-api", wantChannel: apiChannel},
	}

	remote := make(chan config.Message, len(tests))
	api := newTestAPI(remote)

	for _, tt := range tests {
		body := `{"text":"` + tt.text + `","username":"user","gateway":"gateway1"` + tt.channelJSON + `}`
		req := httptest.NewRequest(http.MethodPost, "/api/message", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		ctx := echo.New().NewContext(req, rec)

		require.NoError(t, api.handlePostMessage(ctx))
	}

	received := make(map[string]config.Message, len(tests))

	for range tests {
		message := <-remote
		received[message.Text] = message
	}

	require.Len(t, received, len(tests))

	for _, tt := range tests {
		message, ok := received[tt.text]
		require.True(t, ok, "missing message %q", tt.text)
		require.Equal(t, tt.wantChannel, message.Channel)
	}
}

func TestHandleWebsocketChannel(t *testing.T) {
	tests := []struct {
		channelJSON string
		text        string
		wantChannel string
	}{
		{channelJSON: channelOmitted, text: "ws-channel omitted", wantChannel: apiChannel},
		{channelJSON: `,"channel":"` + channel2 + `"`, text: "ws-channel2", wantChannel: channel2},
		{channelJSON: `,"channel":"` + apiChannel + `"`, text: "ws-api", wantChannel: apiChannel},
	}

	remote := make(chan config.Message, len(tests))
	api := newTestAPI(remote)
	e := echo.New()
	e.GET("/", api.handleWebsocket)

	server := httptest.NewServer(e)
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	conn, response, err := websocket.DefaultDialer.Dial(url, nil)

	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}

	require.NoError(t, err)

	for _, tt := range tests {
		body := `{"text":"` + tt.text + `","username":"user","gateway":"gateway1"` + tt.channelJSON + `}`
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(body)))
	}

	require.NoError(t, conn.Close())

	received := make(map[string]config.Message, len(tests))

	for range tests {
		select {
		case message := <-remote:
			received[message.Text] = message
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for WebSocket messages")
		}
	}

	require.Len(t, received, len(tests))

	for _, tt := range tests {
		message, ok := received[tt.text]
		require.True(t, ok, "missing message %q", tt.text)
		require.Equal(t, tt.wantChannel, message.Channel)
	}
}

func newTestAPI(remote chan config.Message) *API {
	logger := logrus.New()
	br := &bridge.Bridge{} //nolint:exhaustruct_v5
	br.Account = "api.test"
	br.Log = logger.WithField("prefix", "api")

	api := &API{} //nolint:exhaustruct_v5
	api.Config = &bridge.Config{
		Bridge: br,
		Remote: remote,
	}
	api.mrouter = melody.New()

	api.mrouter.HandleMessage(func(s *melody.Session, msg []byte) {
		var message config.Message

		message.Channel = apiChannel

		err := json.Unmarshal(msg, &message) //nolint:musttag
		if err != nil {
			return
		}

		api.handleWebsocketMessage(message, s)
	})

	return api
}
