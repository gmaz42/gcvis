// gcvis is a tool to assist you visualising the operation of
// the go runtime garbage collector.
//
// usage:
//
//     gcvis program [arguments]...
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh/terminal"
)

var iface = flag.String("i", "127.0.0.1", "specify interface to use. defaults to 127.0.0.1.")
var port = flag.String("p", "4500", "specify port to use.")
var serviceName = flag.String("s", "example", "specify service name to include in generated log lines")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s: command <args>...\n", os.Args[0])
		flag.PrintDefaults()
	}

	var pipeRead io.ReadCloser
	var subcommand *SubCommand

	flag.Parse()
	if len(flag.Args()) < 1 {
		if terminal.IsTerminal(int(os.Stdin.Fd())) {
			flag.Usage()
			return
		} else {
			pipeRead = os.Stdin
		}
	} else {
		subcommand = NewSubCommand(flag.Args())
		pipeRead = subcommand.PipeRead
		go subcommand.Run()
	}

	parser := NewParser(pipeRead)

	title := strings.Join(flag.Args(), " ")
	if len(title) == 0 {
		title = fmt.Sprintf("%s:%s", *iface, *port)
	}

	gcvisGraph := NewGraph(title, GCVIS_TMPL)
	server := NewHttpServer(*iface, *port, &gcvisGraph)

	go parser.Run()
	go server.Start()

	url := server.Url()

	log.Printf("server started on %s", url)

	for {
		select {
		case gcTrace := <-parser.GcChan:
			// generate a Loki-compatible JSON output line using this trace
			generateLokiLogLine(gcTrace)

			gcvisGraph.AddGCTraceGraphPoint(gcTrace)
		case scvgTrace := <-parser.ScvgChan:
			// we do not ingest these for Prometheus
			gcvisGraph.AddScavengerGraphPoint(scvgTrace)
		case output := <-parser.NoMatchChan:
			fmt.Fprintln(os.Stderr, output)
		case <-parser.done:
			if parser.Err != nil {
				fmt.Fprintf(os.Stderr, parser.Err.Error())
				os.Exit(1)
			}

			goto out
		}
	}
out:

	if subcommand != nil && subcommand.Err() != nil {
		fmt.Fprintf(os.Stderr, subcommand.Err().Error())
		os.Exit(1)
	}
}

// `{"lvl":"info","host":%q,"srv":"some-service-name","component":"gcvis","time":"%s","msg":%q}`, host, "2021-11-03T14:21:38.783992927Z", msg
type logLine struct {
	Level     string `json:"lvl"`
	Host      string `json:"host"`
	Service   string `json:"srv"`
	Component string `json:"component"`
	// Time is overriden with the calculated time. This timestamp must be formatted as UTC RFC3339
	Time    time.Time `json:"time"`
	Message string    `json:"msg"`

	GC struct {
		HeapUse                                                                              int64
		STWSclock, MASclock, STWMclock, STWScpu, MASAssistcpu, MASBGcpu, MASIdlecpu, STWMcpu float64
	} `json:"gc"`
}

var ownHost string

func init() {
	ownHost, _ = os.Hostname()
}

func generateLokiLogLine(t *gctrace) {
	var l logLine
	l.Level = "info"
	l.Host = ownHost
	l.Service = *serviceName
	l.Component = "gcvis"
	l.Message = "garbage collection event"

	// precision is milliseconds thus we can use this conversion here
	deltaMs := time.Millisecond * time.Duration(int64(t.ElapsedTime*1000))

	l.Time = StartTime.Add(deltaMs).UTC()

	// add harvested fields
	l.GC.HeapUse = t.Heap1
	l.GC.MASAssistcpu = t.MASAssistcpu
	l.GC.MASBGcpu = t.MASBGcpu
	l.GC.MASIdlecpu = t.MASIdlecpu
	l.GC.MASclock = t.MASclock
	l.GC.STWMclock = t.STWMclock
	l.GC.STWMcpu = t.STWMcpu
	l.GC.STWSclock = t.STWSclock
	l.GC.STWScpu = t.STWScpu

	err := json.NewEncoder(os.Stderr).Encode(&l)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot encode log line: %v\n", err)
	}
}
