package main

import (
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
)

func main() {
	var follow bool
	var target float64
	var host string
	var ws *websocket.Conn
	var wsDialer *websocket.Dialer
	var resp *http.Response
	var err error
	var runWebSocket bool
	var wg *sync.WaitGroup

	infoLog := log.New(os.Stdout, "", 0)
	errLog := log.New(os.Stderr, "", log.LstdFlags)

	flag.BoolVar(&follow, "f", false, "follow the device status live (default: false)")
	flag.Float64Var(&target, "t", -1.0, "set the target temperature, negative values will be ignored (default: -1.0)")
	flag.StringVar(&host, "h", "127.0.0.1", "hostname of the device (default: 127.0.0.1)")

	flag.Parse()

	wg = &sync.WaitGroup{}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	if follow {
		wsDialer = &websocket.Dialer{}
		ws, _, err = wsDialer.Dial("ws://"+host+"/ws", nil)
		if err != nil {
			errLog.Fatalf("error while dialing websocket connection")
		}
		go func() {
			runWebSocket = true
			wg.Add(1)
			for runWebSocket {
				_, data, err := ws.ReadMessage()
				if err != nil {
					ws.Close()
					errLog.Fatalf("error while reading websocket message from device\nexit\n")
				}
				infoLog.Println(string(data))
			}
			wg.Done()
		}()
	}

	if target >= 0.0 {
		query := fmt.Sprintf("?target=%.2f", target)
		req, err := http.NewRequest("POST", "http://"+host+"/"+query, nil)
		if err != nil {
			errLog.Printf("error while creating request for setting target temperature: %s\n", err.Error())
		}
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			errLog.Printf("error while carrying out request for setting target temperature: %s\n", err.Error())
		}
		if resp.StatusCode != http.StatusOK {
			errLog.Printf("error while carrying out request for setting target temperature: received non-200 status code:  %s\n", resp.Status)
		}
	}

	if !follow {
		frame := func() []byte {
			resp, err = http.DefaultClient.Get("http://" + host + "/")
			if err != nil {
				errLog.Printf("error while requesting device status: %s", err.Error())
				return nil
			}
			if resp.StatusCode != http.StatusOK {
				errLog.Printf("error while requesting device status: received non-200 status code: %s\n")
				return nil
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				errLog.Printf("error while decoding json response from device: %s", err.Error())
				return nil
			}
			return body
		}()
		if frame != nil {
			infoLog.Println(string(frame))
		} else {
			errLog.Printf("error while getting frame; couldn't determine the error though ...\n")
		}
	}

	if follow {
		<-sig
		runWebSocket = false
		wg.Wait()
		os.Exit(0)
	}
	os.Exit(0)
}
