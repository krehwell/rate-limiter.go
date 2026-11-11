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

const window = 60

var tierNames = [3]string{"ip", "user", "global"} // checked in this order
var budgets = [3]int{5, 3, 12}                     // budget per window, per tier

var counts = map[string]int{} // "tier|identity|window start" -> requests that tier has counted

func handle(r *Request) {
	user := header(r, "x-api-key")
	if user == "" {
		user = "anonymous"
	}
	// TODO 1: the client address is the LAST comma-separated entry of x-forwarded-for, trimmed; "unknown" if there is no header.
	ip := "unknown"
	if xff := header(r, "x-forwarded-for"); xff != "" {
		parts := strings.Split(xff, ",")
		ip = strings.TrimSpace(parts[len(parts)-1])
	}

	// TODO 2: for each tier, in order, with the counter for (tier, that tier's identity, window start) in counts:
	//         at its budget -> 429 with Retry-After (seconds to the window end, rounded up) and
	//         X-RateLimit-Resource naming the tier; stop, and do not touch the later tiers.
	//         Otherwise count the request in that tier and move on to the next one.
	start := int64(math.Floor(r.Now/window)) * window
	identities := [3]string{ip, user, "all"}
	for i, tier := range tierNames {
		key := tier + "|" + identities[i] + "|" + strconv.FormatInt(start, 10)
		if counts[key] >= budgets[i] {
			statusLine(429)
			addHeaderInt("Retry-After", int64(math.Ceil(float64(start+window)-r.Now)))
			addHeader("X-RateLimit-Resource", tier)
			endHead()
			return
		}
		counts[key]++
	}

	// TODO 3: every tier passed -> 200.
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
