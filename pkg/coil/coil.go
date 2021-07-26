package coil

import (
	"errors"
	"io/ioutil"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/felixge/pidctrl"
)

var (
	ErrLostConn error   = errors.New("lost connection to thermocouple")
	MaxTempDiff float64 = 100.0
)

type CoilFrame struct {
	Temp          float64
	Target        float64
	FrameStart    time.Time
	FrameDuration int64 // milliseconds
	FireTime      int64 // milliseconds
}

type Coil struct {
	// Used to interface with device files
	tempf *os.File
	statf *os.File
	tempb []byte
	statb []byte

	window time.Duration

	pid           *pidctrl.PIDController
	errLog        *log.Logger
	infoLog       *log.Logger
	nonInitialRun bool

	WaitGroup *sync.WaitGroup

	Running          bool
	Stop             chan struct{}
	SetTarget        chan float64
	Temp             float64
	LastUpdated      time.Time
	Firing           bool
	FireTime         time.Duration
	CurrentFrameChan chan CoilFrame
	CurrentFrame     CoilFrame
}

func NewCoil(errLog, infoLog *log.Logger) (*Coil, error) {
	var err error
	c := &Coil{
		tempb:            make([]byte, 6),
		statb:            make([]byte, 3),
		errLog:           errLog,
		infoLog:          infoLog,
		Stop:             make(chan struct{}),
		SetTarget:        make(chan float64),
		CurrentFrameChan: make(chan CoilFrame),
	}

	// Grab PID parameters: P, I, D, MAX
	s := os.Getenv("PI_HEATER_PID_P")
	p, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, errors.New("error while parsing PI_HEATER_PID_P: " + err.Error())
	}
	s = os.Getenv("PI_HEATER_PID_I")
	i, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, errors.New("error while parsing PI_HEATER_PID_I: " + err.Error())
	}
	s = os.Getenv("PI_HEATER_PID_D")
	d, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, errors.New("error while parsing PI_HEATER_PID_D: " + err.Error())
	}
	s = os.Getenv("PI_HEATER_PID_MAX")
	max, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, errors.New("error while parsing PI_HEATER_PID_MAX: " + err.Error())
	}
	// Make the window clamp a bit smaller to give some wiggle room and avoid writing to dev file from different goroutines.
	adjustedMax := float64(max - 15)
	c.pid = pidctrl.NewPIDController(p, i, d).SetOutputLimits(0, adjustedMax)
	infoLog.Printf("P.I.D. controller: p=%.3f i=%.3f d=%.3f adjusted_max=%.0f milliseconds\n",
		p, i, d, adjustedMax,
	)

	c.window = time.Duration(max) * time.Millisecond
	devfile := os.Getenv("PI_HEATER_TEMP_DEV_FILE")
	c.tempf, err = os.OpenFile(devfile, os.O_RDONLY, os.ModeDevice)
	if err != nil {
		return nil, err
	}

	devfile = os.Getenv("PI_HEATER_STATUS_DEV_FILE")
	c.statf, err = os.OpenFile(devfile, os.O_RDWR, os.ModeDevice)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Coil) Run() {
	c.infoLog.Printf("starting coil run loop\n")
	var err error
	name := os.Args[0]
	if c.errLog == nil {
		c.errLog = log.New(os.Stderr, name+" ERROR: ", log.LstdFlags|log.Lshortfile)
	}
	if c.infoLog == nil {
		c.infoLog = log.New(ioutil.Discard, name+" INFO: ", log.LstdFlags|log.Lshortfile)
	}
	clock := time.Tick(c.window)
	c.Running = true
	c.WaitGroup.Add(1)
	defer func() {
		c.statf.Close()
		c.tempf.Close()
	}()

	cancelOnOff := make(chan struct{})
	for c.Running {
		select {
		case <-clock:
			oldTemp := c.Temp
			err = c.updateTemp()
			if err != nil {
				c.errLog.Printf("error while updating coil temp: %s\nexiting...\n", err.Error())
				c.pid.Set(0)
				c.Stop <- struct{}{}
			}

			// Make sure the temp hasnt spiked due to tehrmocouple issues
			if math.Abs(oldTemp-c.Temp) >= MaxTempDiff && c.nonInitialRun {
				c.errLog.Println("lost connection to thermocouple")
				c.pid.Set(0)
				c.Stop <- struct{}{}
			}

			c.nonInitialRun = true

			c.FireTime = time.Duration(c.pid.Update(c.Temp)) * time.Millisecond
			c.infoLog.Printf("pulsing coil: %+v\n", c.FireTime)
			frameStart := time.Now()

			// Pulse the coil
			go func() {
				err = c.OnOff(cancelOnOff, c.FireTime)
				if err != nil {
					c.errLog.Printf("error while pulsing coil: %s\nexiting...\n", err.Error())
					c.pid.Set(0)
					c.Stop <- struct{}{}
				}
			}()

			// Send out this time slice's frame
			go func() {
				frame := CoilFrame{
					Temp:          c.Temp,
					Target:        c.pid.Get(),
					FrameStart:    frameStart,
					FrameDuration: c.window.Milliseconds(),
					FireTime:      c.FireTime.Milliseconds(),
				}
				select {
				// If the previous frame is still in the channel, flush it out and send in a new one
				case <-c.CurrentFrameChan:
					c.CurrentFrameChan <- frame
				case c.CurrentFrameChan <- frame:
				}
				c.CurrentFrame = frame
			}()

		case target := <-c.SetTarget:
			c.pid.Set(target)
			c.infoLog.Printf("set new target for coil temperature: %.2ff\n", target)
		case <-c.Stop:
			c.pid.Set(0)
			cancelOnOff <- struct{}{}
			_, err = c.statf.Write([]byte("0"))
			if err != nil {
				c.errLog.Printf("error while shutting off coil, panicking: %s\n", err.Error())
				panic(err)
			}
			c.Firing = false
			c.Running = false
			if c.WaitGroup != nil {
				c.WaitGroup.Done()
			}
			c.infoLog.Printf("stopped coil run loop\n")
			return
		}
	}
}

func (c *Coil) updateTemp() error {
	_, err := c.tempf.Read(c.tempb)
	if err != nil {
		return err
	}
	ts := string(c.tempb)
	ts = strings.TrimRightFunc(ts, trimTest)
	t, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return err
	}
	c.Temp = ((9.0)/(20.0))*t + 32.0
	c.LastUpdated = time.Now()
	c.infoLog.Printf("updated coil temperature: %.2ff\n", c.Temp)
	return nil
}

func (c *Coil) OnOff(cancel chan struct{}, d time.Duration) error {
	_, err := c.statf.Write([]byte("1"))
	if err != nil {
		return err
	}
	c.Firing = true
	timer := time.After(d)
	select {
	case <-timer:
	case <-cancel:
		return nil
	}
	_, err = c.statf.Write([]byte("0"))
	c.Firing = false
	return err
}

func trimTest(c rune) bool {
	return !unicode.IsNumber(c)
}
