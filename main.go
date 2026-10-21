package main

import (
	"io/ioutil"
	"math"
	"os"
	"strconv"
	"strings"
)

// ---- Given: the HTTP plumbing. Read it, then leave it alone. ----

type Request struct {
	Now     float64           // simulated clock, seconds since the epoch
	Method  string            // "GET", "POST", ...
	Target  string            // request target as sent, query string included
	Headers map[string]string // lower-case name -> value, spaces trimmed
}

// header returns a request header's value (pass the name in lower case), or "" if it is absent.
func header(r *Request, name string) string {
	return r.Headers[name]
}

var _ = math.Floor // math is imported for your use; this keeps the import legal if you do not need it

var out strings.Builder

func reason(code int) string {
	switch code {
	case 200:
		return "OK"
	case 429:
		return "Too Many Requests"
	}
	return "Unknown"
}

func statusLine(code int) {
	out.WriteString("HTTP/1.1 " + strconv.Itoa(code) + " " + reason(code) + "\r\n")
}

func addHeader(name, value string) {
	out.WriteString(name + ": " + value + "\r\n")
}

func addHeaderInt(name string, value int64) {
	out.WriteString(name + ": " + strconv.FormatInt(value, 10) + "\r\n")
}

func endHead() {
	out.WriteString("Content-Length: 0\r\n\r\n")
}

const capacity = 10.0  // most tokens a bucket can hold
const rate = 0.5       // tokens added per second
const exportCost = 5.0 // tokens a request to /export spends; every other request spends 1

type bucket struct {
	tokens float64
	last   float64 // time the tokens were last computed
}

var buckets = map[string]*bucket{}

func handle(r *Request) {
	client := header(r, "x-api-key")
	if client == "" {
		client = "anonymous"
	}
	path := r.Target
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	cost := 1.0
	if path == "/export" {
		cost = exportCost
	}
	// TODO 1: a client's first request finds a FULL bucket (tokens: capacity, last: r.Now) in buckets.
	//         Otherwise refill: tokens = min(capacity, tokens + (now - last) * rate), last = now (refused requests too).
	b := buckets[client]
	if b == nil {
		b = &bucket{tokens: capacity, last: r.Now}
		buckets[client] = b
	} else {
		b.tokens = math.Min(capacity, b.tokens+(r.Now-b.last)*rate)
		b.last = r.Now
	}

	// TODO 2: tokens >= cost -> subtract cost, 200. Otherwise 429 with Retry-After = (cost - tokens) / rate, rounded up.
	if b.tokens >= cost {
		b.tokens = b.tokens - cost
		statusLine(200)
	} else {
		statusLine(429)
		addHeaderInt("Retry-After", int64(math.Ceil((cost-b.tokens)/rate)))
	}

	// TODO 3: X-RateLimit-Limit, -Remaining (tokens rounded down), -Reset (now + (capacity - tokens) / rate, rounded up).
	addHeaderInt("X-RateLimit-Limit", capacity)
	addHeaderInt("X-RateLimit-Remaining", int64(math.Floor(b.tokens)))
	addHeaderInt("X-RateLimit-Reset", int64(math.Ceil(r.Now+(capacity-b.tokens)/rate)))
	endHead()
}

func directive(line string, now float64) {}

func main() {
	raw, _ := ioutil.ReadAll(os.Stdin)
	data := string(raw)
	pos := 0
	now := 0.0
	for pos < len(data) {
		if data[pos] == '@' {
			eol := strings.IndexByte(data[pos:], '\n')
			if eol < 0 {
				eol = len(data)
			} else {
				eol += pos
			}
			line := strings.TrimSpace(data[pos:eol])
			if strings.HasPrefix(line, "@now ") {
				now, _ = strconv.ParseFloat(strings.TrimSpace(line[5:]), 64)
			} else {
				directive(line, now)
			}
			pos = eol + 1
			continue
		}
		rel := strings.Index(data[pos:], "\r\n\r\n")
		if rel < 0 {
			break // an unfinished request head gets no reply
		}
		end := pos + rel
		lines := strings.Split(data[pos:end], "\r\n")
		pos = end + 4
		words := strings.Split(lines[0], " ")
		req := &Request{Now: now, Method: words[0], Headers: map[string]string{}}
		if len(words) > 1 {
			req.Target = words[1]
		}
		for _, text := range lines[1:] {
			colon := strings.IndexByte(text, ':')
			if colon <= 0 {
				continue
			}
			name := strings.ToLower(text[:colon])
			value := strings.Trim(text[colon+1:], " \t")
			if old, ok := req.Headers[name]; ok {
				value = old + ", " + value
			}
			req.Headers[name] = value
		}
		handle(req)
	}
	os.Stdout.WriteString(out.String())
}
