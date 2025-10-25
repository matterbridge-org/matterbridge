package main

import (
	// "bytes"
	"flag"
	"fmt"
	// "net/http"
	// "io"
	// "context"
	// "encoding/xml"
	// "errors"
	"crypto/tls"
	"os"
	"strconv"
	"time"

	"github.com/xmppo/go-xmpp"
)

func scenario_outgoing_message(client *xmpp.Client) error {
	for {
		m, err := client.Recv()
		if err != nil {
			// TODO: Some errors should not be fatal
			return err
		}

		switch v := m.(type) {
		case xmpp.Chat:
			if v.Type == "groupchat" && v.Remote == "test@muc.matterbridge-test.localhost/matterbridge-xmpp" && v.Text == "<apitest> outgoing-message-test" {
				// Test successful
				return nil
			} else {
				fmt.Printf("Received MUC message from %s:\n%s\n", v.Remote, v.Text)
			}
		default:
			// Do nothing
		}
	}

	return nil
}

func apply_scenario(scenario string, client *xmpp.Client) error {
	switch scenario {
	case "outgoing-message":
		return scenario_outgoing_message(client)
	default:
		return fmt.Errorf("ERROR: Unknown test scenario: %s\n", scenario)
	}

	return nil
}

func apply_scenario_timeout(scenario string, timeout int, client *xmpp.Client) error {
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
		return nil
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

	// client.JoinMUCNoHistory("test@muc.matterbridge-test.localhost", "client-xmpp")
	client.JoinOrCreateMUCNoHistoryDoNotUseOutsideTests("test@muc.matterbridge-test.localhost", "client-xmpp")

	fmt.Printf("Running scenario %s (timeout=%ds)\n", scenario, timeout)

	err = apply_scenario_timeout(scenario, timeout, client)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func NewClient() (*xmpp.Client, error) {
	options := xmpp.Options{
		Host:                         "localhost:52222",
		User:                         "client-xmpp@matterbridge-test.localhost",
		Password:                     "testxmpp_password",
		NoTLS:                        true,
		StartTLS:                     false,
		Debug:                        true,
		Session:                      true,
		InsecureAllowUnencryptedAuth: true,
		TLSConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	return options.NewClient()
}
