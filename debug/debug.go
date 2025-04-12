package debug

import (
	"log"
	"os"
	"strconv"
)

var (
	Debug bool
)

func init() {
	debugEnv, exists := os.LookupEnv("SOCKET_GO_DEBUG")
	if exists {
		if val, err := strconv.ParseBool(debugEnv); err == nil {
			Debug = val
		}
	}
}

func Printf(format string, v ...interface{}) {
	if Debug {
		log.Printf(format, v...)
	}
}

func Enable() {
	Debug = true
}

func Disable() {
	Debug = false
}
