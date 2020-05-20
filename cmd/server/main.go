// PiHeater - Keep thing toasty
//
// Author: Raphael Reyna
//
// Environment Variables:
// PI_HEATER_TEMP_DEV_FILE - Device file from which to read temperature
// PI_HEATER_STATUS_DEV_FILE - Device file from which to turn coil on and off
// PI_HEATER_START_TEMP - Temperature to heat coil to on start
// PI_HEATER_PID_P - P parameter for PID controller
// PI_HEATER_PID_I - I parameter for PID controller
// PI_HEATER_PID_D - D parameter for PID controller
// PI_HEATER_PID_MAX - Max value clamp on PID controller value
// PI_HEATER_HTTP_PORT - Port over which to serve HTTP traffic

package main

import (
	"github.com/raphaelreyna/pi-heater/pkg/coil"
	"github.com/raphaelreyna/pi-heater/internal/http-server"
	"github.com/raphaelreyna/pi-heater/internal/websocket-hub"
	"flag"
	"github.com/joho/godotenv"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
)

func main() {
	godotenv.Load()
	name := os.Args[0]
	errLog := log.New(os.Stderr, name+" ERROR: ", log.LstdFlags|log.Lshortfile)
	infoLog := log.New(os.Stdout, name+" INFO: ", log.LstdFlags)

	wg := &sync.WaitGroup{}
	c, err := coil.NewCoil(errLog, infoLog)
	if err != nil {
		panic(err)
	}

	c.WaitGroup = wg

	go c.Run()

	setStartingTemp(c, infoLog, errLog)

	wsHub := hub.NewHub(c, infoLog, errLog)
	wsHub.WaitGroup = wg
	go wsHub.Run()

	s := server.NewServer(c, wsHub, errLog, infoLog)
	port := os.Getenv("PI_HEATER_HTTP_PORT")
	infoLog.Printf("starting HTTP server; listening on port %s\n", port)
	go func() {
		err := http.ListenAndServe(":"+port, s)
		if err != nil {
			errLog.Printf("error from http server: %s\n", err.Error())
			c.Stop <- struct{}{}
			wsHub.Stop <- struct{}{}
			wg.Wait()
			os.Exit(0)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	<-sig
	infoLog.Printf("received kill signal\n")
	c.Stop <- struct{}{}
	wsHub.Stop <- struct{}{}
	wg.Wait()
	os.Exit(0)
}

func setStartingTemp(c *coil.Coil, infoLog, errLog *log.Logger) {
	var st float64
	var err error
	flag.Float64Var(&st, "t", 0, "temperature")
	flag.Parse()
	if st == 0 {
		startTempS := os.Getenv("PI_HEATER_START_TEMP")
		st, err = strconv.ParseFloat(startTempS, 64)
		if err != nil {
			errLog.Fatalf("could not determine starting temperature from PI_HEATER_START_TEMP environment variable")
		}
	}
	infoLog.Printf("setting initial temperature to %.2ff\n", st)
	c.SetTarget <- st
}
