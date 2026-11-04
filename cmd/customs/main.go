// Command customs shows what LLM requests look like at every hop and tests gateways against real issues.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

const usage = `customs - see every hop of an LLM request

usage:
  customs up [--open] [--target NAME=URL ...]  start the recorder, relay and web UI
  customs status | down | version
  customs target add NAME URL
  customs run --target NAME -- command      run a command through the relay (env vars for that process only)
  customs traces list|show
  customs report ID [--md]
  customs guide                              instructions for an AI agent
  customs testset validate|list|stats [--dir DIR]
  customs fakeup --listen ADDR
  customs probe plan --driver litellm|magpie --url URL    propose temporary test models (needs approval in the UI)
  customs probe run --plan ID | --url URL                 run the test set against a gateway
  customs probe cleanup                                   remove test models left behind

Run "customs <command> -h" for the flags of a command.
`

func main() {
	if len(os.Args) < 2 && runtime.GOOS == "windows" {
		// Double-clicking customs.exe should start the program, not flash a usage message.
		os.Args = append(os.Args, "up", "--open")
	}
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "testset":
		err = cmdTestset(os.Args[2:])
	case "fakeup":
		err = cmdFakeup(os.Args[2:])
	case "probe":
		err = cmdProbe(os.Args[2:])
	case "up":
		err = cmdUp(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "down":
		err = cmdDown(os.Args[2:])
	case "target":
		err = cmdTarget(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "traces":
		err = cmdTraces(os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	case "guide":
		err = cmdGuide(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
		return
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err == nil && findings {
		os.Exit(10)
	}
	if err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			if ee.msg != "" {
				fmt.Fprintln(os.Stderr, "error:", ee.msg)
			}
			os.Exit(ee.code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
