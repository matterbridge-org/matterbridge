package main

import (
    "bytes"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "net/http"
    "io"
    "os"
    "strconv"
    "time"
)

type Api struct {
    BaseUrl string
}

func NewApi() *Api {
    s := Api{ BaseUrl: "http://localhost:4242/api" }
    return &s
}

func (a *Api) SendMessage(message *Message) error {
    endpoint := fmt.Sprintf("%s/message", a.BaseUrl)
    json_msg, err := json.Marshal(message)
    if err != nil {
        fmt.Printf("Failed to serialize message:\n%s", err)
        return err
    }

    req, err := http.NewRequest("POST", endpoint, bytes.NewReader(json_msg))
    // req.Header.Set("X-Custom-Header", "myvalue")
    req.Header.Set("Content-Type", "application/json")

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    fmt.Println("response Status:", resp.Status)
    fmt.Println("response Headers:", resp.Header)
    body, _ := io.ReadAll(resp.Body)
    fmt.Println("response Body:", string(body))

    if resp.StatusCode != 200 {
        return errors.New("Failed to POST message to the API")
    }

    return nil
}

type Message struct {
    Text string `json:"text"`
    Channel string `json:"channel"`
    Username string `json:"username"`
    UserId string `json:"userid"`
    Avatar string `json:"avatar"`
    Account string `json:"account"`
    Event string `json:"event"`
    Protocol string `json:"protocol"`
    Gateway string `json:"gateway"`
    ParentId string `json:"parent_id"`
    Timestamp string `json:"timestamp"`
    Id string `json:"id"`
    Extra map[string][]interface {} `json:"Extra"`
}

func newMessage(text string) *Message {
    s:= Message {
        Text: text,
        Channel: "testchannel",
        Username: "apitest",
        UserId: "apitest_id",
        Account: "api.test",
        Protocol: "api",
        Gateway: "test",
        Timestamp: "2019-01-09T22:53:51.618575236+01:00",
    }

    return &s
}

func scenario_outgoing_message(api *Api) error {
    err := api.SendMessage(newMessage("outgoing-message-test"))
    if err != nil {
        return err
    }

    return nil
}

func apply_scenario(scenario string, api *Api) error {
	switch scenario {
	case "outgoing-message":
	    return scenario_outgoing_message(api)
	default:
	    fmt.Printf("ERROR: Unknown test scenario: %s\n", scenario)
	    os.Exit(1)
    }

    return nil
}

func apply_scenario_timeout(scenario string, timeout int, api *Api) error {
    timeoutChan := make(chan error, 1)

    go func() {
        timeoutChan <- apply_scenario(scenario, api)
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

    fmt.Println("Initializing API")
    api := NewApi()

    fmt.Printf("Running scenario %s (timeout=%ds)\n", scenario, timeout)

    err := apply_scenario_timeout(scenario, timeout, api)
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
}
