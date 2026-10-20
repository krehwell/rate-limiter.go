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

const limit = 5   // requests allowed per client per window
const window = 60 // window length in seconds

type windowCount struct {
	start int64 // start of the window this client last spoke in
	count int   // requests counted in that window
}

var counts = map[string]*windowCount{}

func handle(r *Request) {
	// TODO 1: the c is the X-API-Key value ("x-api-key" for header()); "anonymous" if it is missing or empty.
	c := header(r, "x-api-key")
	if c == "" {
		c = "anonymous"
	}

	start := int64(math.Floor(r.Now/window)) * window

	// TODO 2: start of the window containing r.Now: int64(math.Floor(now/window)) * window.
	w, ok := counts[c]
	if !ok || w.start < start {
		w = &windowCount{start: start}
		counts[c] = w
	}

	// TODO 3: a client seen in an older window starts again from zero (counts).
	if w.start < start {
		w = &windowCount{start: start}
		counts[c] = w
	}

	// TODO 4: at limit requests -> 429 with Retry-After (seconds to the window end, rounded up), NOT counted.
	//         Otherwise count the request and answer 200.
	if w.count >= limit {
		statusLine(429)
		addHeaderInt("Retry-After", int64(math.Ceil(float64(start+window)-r.Now)))
		endHead()
		return
	}
	w.count++
	statusLine(200)
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
