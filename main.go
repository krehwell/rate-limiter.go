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

type slot struct {
	value     int64
	hasExpiry bool
	expires   float64 // epoch second at which the key disappears
}

// Store is a stand-in for Redis: integer counters with an optional expiry, on the simulated clock.
// Every call runs to completion before the next one starts, so Incr never loses an update.
type Store struct {
	slots map[string]*slot
}

var store = &Store{slots: map[string]*slot{}}
var redisUp = true // false between "@redis down" and "@redis up"

// purge removes an expired key on access, like Redis does.
func (s *Store) purge(key string, now float64) {
	if sl, ok := s.slots[key]; ok && sl.hasExpiry && now >= sl.expires {
		delete(s.slots, key)
	}
}

// Incr is INCR key: add 1 (a missing key counts as 0) and return the new value.
func (s *Store) Incr(key string, now float64) int64 {
	s.purge(key, now)
	sl := s.slots[key]
	if sl == nil {
		sl = &slot{}
		s.slots[key] = sl
	}
	sl.value++
	return sl.value
}

// ExpireAt is EXPIREAT key at: the key disappears at epoch second at. Nothing happens to a missing key.
func (s *Store) ExpireAt(key string, at, now float64) {
	s.purge(key, now)
	if sl := s.slots[key]; sl != nil {
		sl.hasExpiry = true
		sl.expires = at
	}
}

// Expire is EXPIRE key seconds: the key disappears seconds from now.
func (s *Store) Expire(key string, seconds, now float64) {
	s.ExpireAt(key, now+seconds, now)
}

// Count is DBSIZE: how many keys are alive.
func (s *Store) Count(now float64) int {
	for key := range s.slots {
		s.purge(key, now)
	}
	return len(s.slots)
}


const limit = 10 // requests per client per window, across ALL servers
const window = 60 // seconds

func handle(r *Request) {
	client := header(r, "x-api-key")
	if client == "" {
		client = "anonymous"
	}
	// TODO 1: store down (redisUp is false) -> 200 with no rate-limit headers, and do not touch the store.
	if !redisUp {
		statusLine(200)
		endHead()
		return
	}

	// TODO 2: key "rl:<client>:<window index>", window index = floor(now / window). count := store.Incr(key, r.Now).
	idx := int64(math.Floor(r.Now / window))
	end := (idx + 1) * window
	key := "rl:" + client + ":" + strconv.FormatInt(idx, 10)
	count := store.Incr(key, r.Now)

	// TODO 3: count == 1 -> the key is new: store.ExpireAt(key, window end, r.Now).
	if count == 1 {
		store.ExpireAt(key, float64(end), r.Now)
	}

	// TODO 4: count > limit -> 429 with Retry-After; else 200. Then Limit, Remaining (never below 0), Reset (window end).
	if count > limit {
		statusLine(429)
		addHeaderInt("Retry-After", int64(math.Ceil(float64(end)-r.Now)))
	} else {
		statusLine(200)
	}
	addHeaderInt("X-RateLimit-Limit", limit)
	addHeaderInt("X-RateLimit-Remaining", max(0, limit-count))
	addHeaderInt("X-RateLimit-Reset", end)
	endHead()
}

func directive(line string, now float64) {
	switch line {
	case "@redis down":
		redisUp = false
	case "@redis up":
		redisUp = true
	case "@keys":
		out.WriteString("keys=" + strconv.Itoa(store.Count(now)) + "\n")
	}
}

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
