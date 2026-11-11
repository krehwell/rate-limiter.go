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

const edgeLimit = 40   // requests per IP per window
const window = 60      // seconds
const exportCost = 5.0 // tokens for /export; everything else costs 1

type bucket struct { // one token bucket per user
	plan   string
	tokens float64
	last   float64
}

var edgeCounts = map[string]int{}  // "ip|window start" -> requests counted at the edge
var buckets = map[string]*bucket{} // user -> bucket

func handle(r *Request) {
	user := header(r, "x-api-key")
	if user == "" {
		user = "anonymous"
	}
	path := r.Target
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	// TODO 1: GET /healthz -> 200, nothing else: no counters, no buckets.
	if r.Method == "GET" && path == "/healthz" {
		statusLine(200)
		endHead()
		return
	}

	// TODO 2: edge: the ip is the LAST entry of x-forwarded-for, trimmed ("unknown" if absent). At edgeLimit in this
	//         window -> 429, Retry-After (seconds to the window end, rounded up), X-RateLimit-Resource: ip. Else count it.
	ip := "unknown"
	if xff := header(r, "x-forwarded-for"); xff != "" {
		parts := strings.Split(xff, ",")
		ip = strings.TrimSpace(parts[len(parts)-1])
	}
	start := int64(math.Floor(r.Now/window)) * window
	edgeKey := ip + "|" + strconv.FormatInt(start, 10)
	if edgeCounts[edgeKey] >= edgeLimit {
		statusLine(429)
		addHeaderInt("Retry-After", int64(math.Ceil(float64(start+window)-r.Now)))
		addHeader("X-RateLimit-Resource", "ip")
		endHead()
		return
	}
	edgeCounts[edgeKey]++

	// TODO 3: plan = x-plan if it is "pro" or "enterprise", else "free". free: capacity 5, rate 0.25. pro: capacity 10,
	//         rate 0.5. enterprise -> 200, no bucket (forget the user's old one).
	plan := header(r, "x-plan")
	capacity, rate := 5.0, 0.25
	switch plan {
	case "pro":
		capacity, rate = 10.0, 0.5
	case "enterprise":
		delete(buckets, user)
		statusLine(200)
		endHead()
		return
	default:
		plan = "free"
	}

	// TODO 4: the user's bucket (buckets[user]): new, full and stamped r.Now if missing or made for another plan;
	//         otherwise refill it. tokens >= cost -> subtract, 200. Else 429 + Retry-After (deficit / rate, rounded up)
	//         + X-RateLimit-Resource: user.
	cost := 1.0
	if path == "/export" {
		cost = exportCost
	}
	b := buckets[user]
	if b == nil || b.plan != plan {
		b = &bucket{plan: plan, tokens: capacity, last: r.Now}
		buckets[user] = b
	} else {
		b.tokens = math.Min(capacity, b.tokens+(r.Now-b.last)*rate)
		b.last = r.Now
	}
	if b.tokens >= cost {
		b.tokens -= cost
		statusLine(200)
	} else {
		statusLine(429)
		addHeaderInt("Retry-After", int64(math.Ceil((cost-b.tokens)/rate)))
		addHeader("X-RateLimit-Resource", "user")
	}

	// TODO 5: for free and pro: X-RateLimit-Limit, -Remaining (rounded down), -Reset (now + (capacity - tokens) / rate, rounded up).
	addHeaderInt("X-RateLimit-Limit", int64(capacity))
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
