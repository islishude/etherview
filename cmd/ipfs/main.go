// Command ipfs uploads and downloads individual files through a Kubo RPC API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	cid "github.com/ipfs/go-cid"
)

type options struct {
	command string
	api     string
	timeout time.Duration
	wrap    bool
	output  string
	input   string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ipfs: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	client := newClient()
	defer client.CloseIdleConnections()
	if opts.command == "upload" {
		var hash string
		hash, err = upload(ctx, client, opts)
		if err == nil {
			_, err = fmt.Fprintln(stdout, hash)
		}
	} else {
		err = download(ctx, client, opts)
	}
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		_, _ = fmt.Fprintf(stderr, "ipfs: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var opts options
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprintln(stderr, "usage: ipfs upload [--api URL] [--timeout 5m] [--wrap] FILE")
		_, _ = fmt.Fprintln(stderr, "       ipfs download [--api URL] [--timeout 5m] --output FILE CID[/PATH]")
		if len(args) == 0 {
			return opts, errors.New("a command is required")
		}
		return opts, flag.ErrHelp
	}
	opts.command = args[0]
	if opts.command != "upload" && opts.command != "download" {
		return opts, errors.New("expected upload or download")
	}
	flags := flag.NewFlagSet(opts.command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.api, "api", "http://127.0.0.1:5001", "Kubo RPC HTTP(S) origin")
	flags.DurationVar(&opts.timeout, "timeout", 5*time.Minute, "total operation timeout")
	if opts.command == "upload" {
		flags.BoolVar(&opts.wrap, "wrap", false, "wrap the file in a directory and return its CID")
	} else {
		flags.StringVar(&opts.output, "output", "", "new output file (required; never overwritten)")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return opts, err
	}
	if flags.NArg() != 1 || opts.timeout <= 0 {
		return opts, errors.New("exactly one input and a positive timeout are required")
	}
	u, err := url.Parse(opts.api)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return opts, errors.New("api must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	opts.api = strings.TrimSuffix(opts.api, "/")
	opts.input = flags.Arg(0)
	if opts.command == "download" {
		if opts.output == "" {
			return opts, errors.New("output is required")
		}
		opts.input, err = contentPath(opts.input)
	}
	return opts, err
}

func contentPath(input string) (string, error) {
	if strings.HasPrefix(input, "ipfs://") {
		var err error
		input, err = decodeIPFSURI(input)
		if err != nil {
			return "", err
		}
	}
	// A bare CID/path is a literal content path, not a URL: %, #, ? and +
	// are valid filename characters and must survive RPC query encoding.
	if strings.Contains(input, "\\") || strings.IndexFunc(input, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return "", errors.New("invalid IPFS path")
	}
	parts := strings.Split(input, "/")
	if _, err := cid.Decode(parts[0]); err != nil {
		return "", errors.New("invalid CID")
	}
	for _, segment := range parts[1:] {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("invalid IPFS path segment")
		}
	}
	return "/ipfs/" + input, nil
}

func decodeIPFSURI(input string) (string, error) {
	target, err := url.Parse(input)
	if err != nil || target.Host == "" || target.User != nil || target.RawQuery != "" || target.ForceQuery || strings.Contains(input, "#") {
		return "", errors.New("invalid IPFS URI")
	}
	segments := strings.Split(target.EscapedPath(), "/")
	for index, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil || strings.Contains(decoded, "/") {
			return "", errors.New("invalid IPFS URI path segment")
		}
		segments[index] = decoded
	}
	// Validate decoded controls, backslashes and dot segments in contentPath.
	return target.Host + strings.Join(segments, "/"), nil
}
