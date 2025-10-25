package main

import (
    // "bytes"
    "flag"
    "fmt"
    // "net/http"
    // "io"
	"context"
	// "encoding/xml"
	// "errors"
	"time"
	"strconv"
	"os"

	"mellium.im/sasl"
	"mellium.im/xmpp"
	// "mellium.im/xmpp/dial"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	// "mellium.im/xmpp/stream"

)

func scenario_outgoing_message(client *xmpp.Session) error {
    // err := api.SendMessage(newMessage("outgoing-message-test"))
    // if err != nil {
    //     return err
    // }

    return nil
}

func apply_scenario(scenario string, client *xmpp.Session) error {
	// switch scenario {
	// case "outgoing-message":
	//     return scenario_outgoing_message(api, client)
	// default:
	//     fmt.Printf("ERROR: Unknown test scenario: %s\n", scenario)
	//     os.Exit(1)
 //    }

    return nil
}

func apply_scenario_timeout(scenario string, timeout int, client *xmpp.Session) error {
    timeoutChan := make(chan error, 1)

    go func() {
        timeoutChan <- apply_scenario(scenario, client)
    }()

    select {
    case err := <-timeoutChan:
	    if err != nil {
	        return fmt.Errorf("ERROR: Scenario %s failed because of error:\n%s", scenario, err)
	    }

	    fmt.Printf("OK: Scenario %s", scenario)
    case <-time.After(time.Duration(timeout) * time.Second):
        return fmt.Errorf("ERROR: Scenario %s timeout after %d seconds", scenario, timeout)
    }

    return nil
}

func main() {
    flag.Parse()
    args := flag.Args()
    scenario := args[0]
    var timeout int
    if len(args) < 2 {
        timeout = 5
    } else {
        timeout2, err := strconv.Atoi(args[1])
        if err != nil {
            fmt.Printf("ERROR: Invalid timeout: %s\n", args[1])
            os.Exit(1)
        }
        timeout = timeout2
    }

    fmt.Println("Initializing client")
    client, err := NewClient()
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }

    fmt.Printf("Running scenario %s (timeout=%ds)\n", scenario, timeout)
   
    err = apply_scenario_timeout(scenario, timeout, client)
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
}

// MessageBody is a message stanza that contains a body. It is normally used for
// chat messages.
type MessageBody struct {
	stanza.Message
	Body string `xml:"body"`
}

func NewClient() (*xmpp.Session, error) {
	j, _ := jid.Parse("xmpp@matterbridge-test.localhost")
	ctx := context.Background()

	s, err := xmpp.DialClientSession(ctx, j, xmpp.BindResource(), xmpp.SASL("", "password", sasl.ScramSha1Plus, sasl.Plain))
	if err != nil {
		return nil, fmt.Errorf("error dialing session: %w", err)
	}

	// s, err := xmpp.NewSession(ctx, j.Domain(), j, conn, 0, xmpp.NewNegotiator(func(*xmpp.Session, *xmpp.StreamConfig) xmpp.StreamConfig {
	// 	return xmpp.StreamConfig{
	// 		Lang: "en",
	// 		Features: []xmpp.StreamFeature{
	// 			xmpp.BindResource(),
	// 			// xmpp.StartTLS(&tls.Config{
	// 			// 	ServerName: j.Domain().String(),
	// 			// 	MinVersion: tls.VersionTLS12,
	// 			// }),
	// 			xmpp.SASL("", "password", sasl.ScramSha1Plus, sasl.ScramSha1, sasl.Plain),
	// 		},
	// 		// TeeIn:  xmlIn,
	// 		// TeeOut: xmlOut,
	// 	}
	// }))

	// if err != nil {
	// 	return nil, return fmt.Errorf("Error connecting: %w", err)
	// }

	defer func() {
		fmt.Println("Closing conn…")
		if err := s.Conn().Close(); err != nil {
			fmt.Printf("Error closing connection: %q", err)
		}
	}()

	// Send initial presence to let the server know we want to receive messages.
	err = s.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil))
	if err != nil {
		return nil, fmt.Errorf("Error sending initial presence: %w", err)
	}

	return s, err
}

