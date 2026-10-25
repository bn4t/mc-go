// mc-conformance runs a portable protocol smoke profile against a TCP endpoint.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mc-go/conformance"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errout io.Writer) int {
	f := flag.NewFlagSet("mc-conformance", flag.ContinueOnError)
	f.SetOutput(errout)
	config := f.String("config", "", "JSON endpoint manifest")
	endpoint := f.String("endpoint", "", "server host:port")
	protocol := f.Int("protocol", 776, "Java protocol version")
	version := f.String("version", "", "expected advertised version (optional)")
	caps := f.String("capabilities", "status,ping", "comma-separated declared capabilities")
	timeout := f.Duration("timeout", 10*time.Second, "per-scenario connection and protocol deadline")
	output := f.String("output", "-", "JSON report file, or - for stdout")
	if e := f.Parse(args); e != nil {
		return 2
	}
	if f.NArg() != 0 {
		fmt.Fprintln(errout, "unexpected positional arguments")
		return 2
	}
	c := conformance.Config{Endpoint: *endpoint, Protocol: int32(*protocol), Version: *version, Capabilities: strings.Split(*caps, ","), Timeout: *timeout}
	if *config != "" {
		b, e := os.ReadFile(*config)
		if e != nil {
			fmt.Fprintln(errout, e)
			return 2
		}
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.DisallowUnknownFields()
		c = conformance.Config{}
		if e = d.Decode(&c); e != nil {
			fmt.Fprintln(errout, e)
			return 2
		}
		if e = d.Decode(new(any)); e != io.EOF {
			fmt.Fprintln(errout, "manifest must contain exactly one JSON object")
			return 2
		}
		f.Visit(func(v *flag.Flag) {
			switch v.Name {
			case "endpoint":
				c.Endpoint = *endpoint
			case "protocol":
				c.Protocol = int32(*protocol)
			case "version":
				c.Version = *version
			case "capabilities":
				c.Capabilities = strings.Split(*caps, ",")
			case "timeout":
				c.Timeout = *timeout
			}
		})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report := conformance.Run(ctx, c)
	var dst io.Writer = out
	var file *os.File
	if *output != "-" {
		var e error
		file, e = os.Create(*output)
		if e != nil {
			fmt.Fprintln(errout, e)
			return 2
		}
		dst = file
	}
	encoder := json.NewEncoder(dst)
	encoder.SetIndent("", "  ")
	if e := encoder.Encode(report); e != nil {
		if file != nil {
			_ = file.Close()
		}
		fmt.Fprintln(errout, e)
		return 2
	}
	if file != nil {
		if e := file.Close(); e != nil {
			fmt.Fprintln(errout, e)
			return 2
		}
	}
	for _, r := range report.Results {
		fmt.Fprintf(errout, "%-14s %-36s %s\n", r.Status, r.Scenario, r.Detail)
	}
	if report.Reason != "" {
		fmt.Fprintln(errout, report.Reason)
	}
	return report.ExitCode()
}
