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

var dayNames = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
var monthNames = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func pad(n int64, width int) string {
	s := strconv.FormatInt(n, 10)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// httpDate turns epoch seconds into an HTTP date (RFC 9110, section 5.6.7),
// e.g. "Tue, 14 Nov 2023 22:13:20 GMT". It drops the fraction.
func httpDate(t float64) string {
	secs := int64(math.Floor(t))
	days := secs / 86400
	sod := secs % 86400
	weekday := (days + 4) % 7 // 1970-01-01 was a Thursday
	z := days + 719468        // civil-from-days: shift the epoch to 0000-03-01
	era := z / 146097
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return dayNames[weekday] + ", " + pad(d, 2) + " " + monthNames[m-1] + " " + pad(y, 4) + " " +
		pad(sod/3600, 2) + ":" + pad(sod%3600/60, 2) + ":" + pad(sod%60, 2) + " GMT"
}


const interval = 1.0 // seconds between releases: the queue drains 1 request per second
const burst = 3      // requests that may wait in the queue

var nextFree = map[string]float64{} // client -> earliest time the next request may be released

func handle(r *Request) {
	client := header(r, "x-api-key")
	if client == "" {
		client = "anonymous"
	}
	if _, seen := nextFree[client]; !seen {
		nextFree[client] = r.Now
	}
	// TODO 1: release = the later of r.Now and nextFree[client] (a new client already has nextFree = r.Now).
	release := math.Max(r.Now, nextFree[client])
	// TODO 2: release - r.Now > burst * interval -> 429 with Retry-After = release - burst * interval - now (rounded up).
	//         Change nothing.
	if release-r.Now > burst*interval {
		statusLine(429)
		addHeaderInt("Retry-After", int64(math.Ceil(release-burst*interval-r.Now)))
		endHead()
		return
	}

	// TODO 3: otherwise nextFree[client] = release + interval, answer 200 with  Date: httpDate(release).
	nextFree[client] = release + interval
	statusLine(200)
	addHeader("Date", httpDate(release))
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
