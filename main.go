package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"

	"gopkg.in/alecthomas/kingpin.v3-unstable"
)

var (
	concurrency = kingpin.Flag("concurrency", "Number of connections to run concurrently").Short('c').Default("1").Int()
	requests    = kingpin.Flag("requests", "Number of requests to run").Short('n').Default("-1").Int64()
	duration    = kingpin.Flag("duration", "Duration of test, examples: -d 10s -d 3m").Short('d').PlaceHolder("DURATION").Duration()
	interval    = kingpin.Flag("interval", "Print snapshot result every interval, use 0 to print once at the end").Short('i').Default("200ms").Duration()
	seconds     = kingpin.Flag("seconds", "Use seconds as time unit to print").Bool()

	body        = kingpin.Flag("body", "HTTP request body, if start the body with @, the rest should be a filename to read").Short('b').String()
	stream      = kingpin.Flag("stream", "Specify whether to stream file specified by '--body @file' using chunked encoding or to read into memory").Default("false").Bool()
	method      = kingpin.Flag("method", "HTTP method").Default("GET").Short('m').String()
	headers     = kingpin.Flag("header", "Custom HTTP headers").Short('H').PlaceHolder("K:V").Strings()
	host        = kingpin.Flag("host", "Host header").String()
	contentType = kingpin.Flag("content", "Content-Type header").Short('T').String()
	cert        = kingpin.Flag("cert", "Path to the client's TLS Certificate").ExistingFile()
	key         = kingpin.Flag("key", "Path to the client's TLS Certificate Private Key").ExistingFile()
	insecure    = kingpin.Flag("insecure", "Controls whether a client verifies the server's certificate chain and host name").Short('k').Bool()

	chartsListenAddr = kingpin.Flag("listen", "Listen addr to serve Web UI").Default(":18888").String()
	timeout          = kingpin.Flag("timeout", "Timeout for each http request").PlaceHolder("DURATION").Duration()
	dialTimeout      = kingpin.Flag("dial-timeout", "Timeout for dial addr").PlaceHolder("DURATION").Duration()
	reqWriteTimeout  = kingpin.Flag("req-timeout", "Timeout for full request writing").PlaceHolder("DURATION").Duration()
	respReadTimeout  = kingpin.Flag("resp-timeout", "Timeout for full response reading").PlaceHolder("DURATION").Duration()
	socks5           = kingpin.Flag("socks5", "Socks5 proxy").PlaceHolder("ip:port").String()

	autoOpenBrowser = kingpin.Flag("auto-open-browser", "Specify whether auto open browser to show Web charts").Bool()
	clean           = kingpin.Flag("clean", "Clean the histogram bar once its finished. Default is true").Default("true").NegatableBool()
	summary         = kingpin.Flag("summary", "Only print the summary without realtime reports").Default("false").NegatableBool()
	url             = kingpin.Arg("url", "request url").Required().String()
)

func errAndExit(msg string) {
	fmt.Fprintln(os.Stderr, "plow: "+msg)
	os.Exit(1)
}

var CompactUsageTemplate = `{{define "FormatCommand" -}}
{{if .FlagSummary}} {{.FlagSummary}}{{end -}}
{{range .Args}} {{if not .Required}}[{{end}}<{{.Name}}>{{if .Value|IsCumulative}} ...{{end}}{{if not .Required}}]{{end}}{{end -}}
{{end -}}

{{define "FormatCommandList" -}}
{{range . -}}
{{if not .Hidden -}}
{{.Depth|Indent}}{{.Name}}{{if .Default}}*{{end}}{{template "FormatCommand" .}}
{{end -}}
{{template "FormatCommandList" .Commands -}}
{{end -}}
{{end -}}

{{define "FormatUsage" -}}
{{template "FormatCommand" .}}{{if .Commands}} <command> [<args> ...]{{end}}
{{if .Help}}
{{.Help|Wrap 0 -}}
{{end -}}

{{end -}}

{{if .Context.SelectedCommand -}}
{{T "usage:"}} {{.App.Name}} {{template "FormatUsage" .Context.SelectedCommand}}
{{else -}}
{{T "usage:"}} {{.App.Name}}{{template "FormatUsage" .App}}
{{end -}}
Examples:

  plow http://127.0.0.1:8080/ -c 20 -n 100000
  plow https://httpbin.org/post -c 20 -d 5m --body @file.json -T 'application/json' -m POST

{{if .Context.Flags -}}
{{T "Flags:"}}
{{.Context.Flags|FlagsToTwoColumns|FormatTwoColumns}}
  Flags default values also read from env PLOW_SOME_FLAG, such as PLOW_TIMEOUT=5s equals to --timeout=5s

{{end -}}
{{if .Context.Args -}}
{{T "Args:"}}
{{.Context.Args|ArgsToTwoColumns|FormatTwoColumns}}
{{end -}}
{{if .Context.SelectedCommand -}}
{{if .Context.SelectedCommand.Commands -}}
{{T "Commands:"}}
  {{.Context.SelectedCommand}}
{{.Context.SelectedCommand.Commands|CommandsToTwoColumns|FormatTwoColumns}}
{{end -}}
{{else if .App.Commands -}}
{{T "Commands:"}}
{{.App.Commands|CommandsToTwoColumns|FormatTwoColumns}}
{{end -}}
`

func main() {
	kingpin.UsageTemplate(CompactUsageTemplate).
		Version("1.1.0").
		Author("six-ddc@github").
		Resolver(kingpin.PrefixedEnvarResolver("PLOW_", ";")).
		Help = `A high-performance HTTP benchmarking tool with real-time web UI and terminal displaying`
	kingpin.Parse()

	if *requests >= 0 && *requests < int64(*concurrency) {
		errAndExit("requests must greater than or equal concurrency")
		return
	}
	if (*cert != "" && *key == "") || (*cert == "" && *key != "") {
		errAndExit("must specify cert and key at the same time")
		return
	}

	var err error
	var bodyBytes []byte
	var bodyFile string
	if strings.HasPrefix(*body, "@") {
		fileName := (*body)[1:]
		if _, err = os.Stat(fileName); err != nil {
			errAndExit(err.Error())
			return
		}
		if *stream {
			bodyFile = fileName
		} else {
			bodyBytes, err = ioutil.ReadFile(fileName)
			if err != nil {
				errAndExit(err.Error())
				return
			}
		}
	} else if *body != "" {
		bodyBytes = []byte(*body)
	}

	clientOpt := ClientOpt{
		url:       *url,
		method:    *method,
		headers:   *headers,
		bodyBytes: bodyBytes,
		bodyFile:  bodyFile,

		certPath: *cert,
		keyPath:  *key,
		insecure: *insecure,

		maxConns:     *concurrency,
		doTimeout:    *timeout,
		readTimeout:  *respReadTimeout,
		writeTimeout: *reqWriteTimeout,
		dialTimeout:  *dialTimeout,

		socks5Proxy: *socks5,
		contentType: *contentType,
		host:        *host,
	}

	requester, err := NewRequester(*concurrency, *requests, *duration, &clientOpt)
	if err != nil {
		errAndExit(err.Error())
		return
	}

	outStream := os.Stdout
	if *summary {
		outStream = os.Stderr
		isTerminal = false
	}
	// description
	var desc string
	desc = fmt.Sprintf("Benchmarking %s", *url)
	if *requests > 0 {
		desc += fmt.Sprintf(" with %d request(s)", *requests)
	}
	if *duration > 0 {
		desc += fmt.Sprintf(" for %s", duration.String())
	}
	desc += fmt.Sprintf(" using %d connection(s).", *concurrency)
	fmt.Fprintln(outStream,desc)

	// charts listener
	var ln net.Listener
	if *chartsListenAddr != "" {
		ln, err = net.Listen("tcp", *chartsListenAddr)
		if err != nil {
			errAndExit(err.Error())
			return
		}
		fmt.Fprintln(outStream,"@ Real-time charts is listening on http://%s", ln.Addr().String())
	}
	fmt.Fprintln(outStream,"")

	// do request
	go requester.Run()

	// metrics collection
	report := NewStreamReport()
	go report.Collect(requester.RecordChan())

	if ln != nil {
		// serve charts data
		charts, err := NewCharts(ln, report.Charts, desc)
		if err != nil {
			errAndExit(err.Error())
			return
		}
		go charts.Serve(*autoOpenBrowser)
	}

	// terminal printer
	printer := NewPrinter(*requests, *duration, !*clean, *summary)
	printer.PrintLoop(report.Snapshot, *interval, *seconds, report.Done())

}
