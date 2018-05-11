package control

import (
	"strconv"
	"strings"
	"time"

	"github.com/cretz/bine/util"
)

type EventCode string

const (
	EventCodeAddrMap       EventCode = "ADDRMAP"
	EventCodeBandwidth     EventCode = "BW"
	EventCodeCircuit       EventCode = "CIRC"
	EventCodeDescChanged   EventCode = "DESCCHANGED"
	EventCodeLogDebug      EventCode = "DEBUG"
	EventCodeLogErr        EventCode = "ERR"
	EventCodeLogInfo       EventCode = "INFO"
	EventCodeLogNotice     EventCode = "NOTICE"
	EventCodeLogWarn       EventCode = "WARN"
	EventCodeNewDesc       EventCode = "NEWDESC"
	EventCodeORConn        EventCode = "ORCONN"
	EventCodeStatusClient  EventCode = "STATUS_CLIENT"
	EventCodeStatusGeneral EventCode = "STATUS_GENERAL"
	EventCodeStatusServer  EventCode = "STATUS_SERVER"
	EventCodeStream        EventCode = "STREAM"
)

func (c *Conn) AddEventListener(events []EventCode, ch chan<- Event) error {
	// TODO: do we want to set the local map first? Or do we want to lock on the net request too?
	c.eventListenersLock.Lock()
	for _, event := range events {
		// Must completely replace the array, never mutate it
		prevArr := c.eventListeners[event]
		newArr := make([]chan<- Event, len(prevArr)+1)
		copy(newArr, prevArr)
		newArr[len(newArr)-1] = ch
		c.eventListeners[event] = newArr
	}
	c.eventListenersLock.Unlock()
	return c.sendSetEvents()
}

func (c *Conn) RemoveEventListener(events []EventCode, ch chan<- Event) error {
	// TODO: do we want to mutate the local map first?
	c.eventListenersLock.Lock()
	for _, event := range events {
		arr := c.eventListeners[event]
		index := -1
		for i, listener := range arr {
			if listener == ch {
				index = i
				break
			}
		}
		if index != -1 {
			if len(arr) == 1 {
				delete(c.eventListeners, event)
			} else {
				// Must completely replace the array, never mutate it
				newArr := make([]chan<- Event, len(arr)-1)
				copy(newArr, arr[:index])
				copy(newArr[index:], arr[index+1:])
				c.eventListeners[event] = newArr
			}
		}
	}
	c.eventListenersLock.Unlock()
	return c.sendSetEvents()
}

func (c *Conn) sendSetEvents() error {
	c.eventListenersLock.RLock()
	cmd := "SETEVENTS"
	for event := range c.eventListeners {
		cmd += " " + string(event)
	}
	c.eventListenersLock.RUnlock()
	return c.sendRequestIgnoreResponse(cmd)
}

func (c *Conn) relayAsyncEvents(resp *Response) {
	code, data, _ := util.PartitionString(resp.Reply, ' ')
	// Only relay if there are chans
	c.eventListenersLock.RLock()
	chans := c.eventListeners[EventCode(code)]
	c.eventListenersLock.RUnlock()
	if len(chans) == 0 {
		return
	}
	// Parse the event
	// TODO: more events
	var event Event
	switch EventCode(code) {
	case EventCodeCircuit:
		event = ParseCircuitEvent(data)
	}
	if event != nil {
		for _, ch := range chans {
			// Just send, if closed or blocking, that's not our problem
			ch <- event
		}
	}
}

// zero on fail
func parseISOTime(str string) time.Time {
	// Essentially time.RFC3339 but without 'T' or TZ info
	const layout = "2006-01-02 15:04:05"
	ret, err := time.Parse(layout, str)
	if err != nil {
		ret = time.Time{}
	}
	return ret
}

// zero on fail
func parseISOTime2Frac(str string) time.Time {
	// Essentially time.RFC3339Nano but without TZ info
	const layout = "2006-01-02T15:04:05.999999999"
	ret, err := time.Parse(layout, str)
	if err != nil {
		ret = time.Time{}
	}
	return ret
}

type Event interface {
	Code() EventCode
}

type CircuitEvent struct {
	Raw           string
	CircuitID     string
	Status        string
	Path          []string
	BuildFlags    []string
	Purpose       string
	HSState       string
	RendQuery     string
	TimeCreated   time.Time
	Reason        string
	RemoteReason  string
	SocksUsername string
	SocksPassword string
}

func ParseCircuitEvent(raw string) *CircuitEvent {
	event := &CircuitEvent{Raw: raw}
	event.CircuitID, raw, _ = util.PartitionString(raw, ' ')
	var ok bool
	event.Status, raw, ok = util.PartitionString(raw, ' ')
	var attr string
	first := true
	for ok {
		attr, raw, ok = util.PartitionString(raw, ' ')
		key, val, _ := util.PartitionString(attr, '=')
		switch key {
		case "BUILD_FLAGS":
			event.BuildFlags = strings.Split(val, ",")
		case "PURPOSE":
			event.Purpose = val
		case "HS_STATE":
			event.HSState = val
		case "REND_QUERY":
			event.RendQuery = val
		case "TIME_CREATED":
			event.TimeCreated = parseISOTime2Frac(val)
		case "REASON":
			event.Reason = val
		case "REMOTE_REASON":
			event.RemoteReason = val
		case "SOCKS_USERNAME":
			event.SocksUsername = val
		case "SOCKS_PASSWORD":
			event.SocksPassword = val
		default:
			if first {
				event.Path = strings.Split(val, ",")
			}
		}
		first = false
	}
	return event
}

func (*CircuitEvent) Code() EventCode { return EventCodeCircuit }

type StreamEvent struct {
	Raw           string
	StreamID      string
	Status        string
	CircuitID     string
	TargetAddress string
	TargetPort    int
	Reason        string
	RemoteReason  string
	Source        string
	SourceAddress string
	SourcePort    int
	Purpose       string
}

func ParseStreamEvent(raw string) *StreamEvent {
	event := &StreamEvent{Raw: raw}
	event.StreamID, raw, _ = util.PartitionString(raw, ' ')
	event.Status, raw, _ = util.PartitionString(raw, ' ')
	event.CircuitID, raw, _ = util.PartitionString(raw, ' ')
	var ok bool
	event.TargetAddress, raw, ok = util.PartitionString(raw, ' ')
	if target, port, hasPort := util.PartitionStringFromEnd(event.TargetAddress, ':'); hasPort {
		event.TargetAddress = target
		event.TargetPort, _ = strconv.Atoi(port)
	}
	var attr string
	for ok {
		attr, raw, ok = util.PartitionString(raw, ' ')
		key, val, _ := util.PartitionString(attr, '=')
		switch key {
		case "REASON":
			event.Reason = val
		case "REMOTE_REASON":
			event.RemoteReason = val
		case "SOURCE":
			event.Source = val
		case "SOURCE_ADDR":
			event.SourceAddress = val
			if source, port, hasPort := util.PartitionStringFromEnd(event.SourceAddress, ':'); hasPort {
				event.SourceAddress = source
				event.SourcePort, _ = strconv.Atoi(port)
			}
		case "PURPOSE":
			event.Purpose = val
		}
	}
	return event
}

func (*StreamEvent) Code() EventCode { return EventCodeStream }

type ORConnEvent struct {
	Raw         string
	Target      string
	Status      string
	Reason      string
	NumCircuits int
	ConnID      string
}

func ParseORConnEvent(raw string) *ORConnEvent {
	event := &ORConnEvent{Raw: raw}
	event.Target, raw, _ = util.PartitionString(raw, ' ')
	var ok bool
	event.Status, raw, ok = util.PartitionString(raw, ' ')
	var attr string
	for ok {
		attr, raw, ok = util.PartitionString(raw, ' ')
		key, val, _ := util.PartitionString(attr, '=')
		switch key {
		case "REASON":
			event.Reason = val
		case "NCIRCS":
			event.NumCircuits, _ = strconv.Atoi(val)
		case "ID":
			event.ConnID = val
		}
	}
	return event
}

func (*ORConnEvent) Code() EventCode { return EventCodeORConn }

type BandwidthEvent struct {
	Raw          string
	BytesRead    int64
	BytesWritten int64
}

func ParseBandwidthEvent(raw string) *BandwidthEvent {
	event := &BandwidthEvent{Raw: raw}
	var temp string
	temp, raw, _ = util.PartitionString(raw, ' ')
	event.BytesRead, _ = strconv.ParseInt(temp, 10, 64)
	temp, raw, _ = util.PartitionString(raw, ' ')
	event.BytesWritten, _ = strconv.ParseInt(temp, 10, 64)
	return event
}

func (*BandwidthEvent) Code() EventCode { return EventCodeBandwidth }

type LogEvent struct {
	Severity EventCode
	Raw      string
}

func ParseLogEvent(severity EventCode, raw string) *LogEvent {
	return &LogEvent{Severity: severity, Raw: raw}
}

func (l *LogEvent) Code() EventCode { return l.Severity }

type NewDescEvent struct {
	Raw   string
	Descs []string
}

func ParseNewDescEvent(raw string) *NewDescEvent {
	return &NewDescEvent{Raw: raw, Descs: strings.Split(raw, " ")}
}

func (*NewDescEvent) Code() EventCode { return EventCodeNewDesc }

type AddrMapEvent struct {
	Raw        string
	Address    string
	NewAddress string
	ErrorCode  string
	// Zero if no expire
	Expires time.Time
	// Sans double quotes
	Cached string
}

func ParseAddrMapEvent(raw string) *AddrMapEvent {
	event := &AddrMapEvent{Raw: raw}
	event.Address, raw, _ = util.PartitionString(raw, ' ')
	event.NewAddress, raw, _ = util.PartitionString(raw, ' ')
	var ok bool
	// Skip local expiration, use UTC one later
	_, raw, ok = util.PartitionString(raw, ' ')
	var attr string
	for ok {
		attr, raw, ok = util.PartitionString(raw, ' ')
		key, val, _ := util.PartitionString(attr, '=')
		switch key {
		case "error":
			event.ErrorCode = val
		case "EXPIRES":
			event.Expires = parseISOTime(val)
		case "CACHED":
			event.Cached, _ = util.UnescapeSimpleQuotedStringIfNeeded(val)
		}
	}
	return event
}

func (*AddrMapEvent) Code() EventCode { return EventCodeAddrMap }

type DescChangedEvent struct {
	Raw string
}

func ParseDescChangedEvent(raw string) *DescChangedEvent {
	return &DescChangedEvent{Raw: raw}
}

func (*DescChangedEvent) Code() EventCode { return EventCodeDescChanged }

type StatusEvent struct {
	Raw       string
	Type      EventCode
	Severity  string
	Action    string
	Arguments map[string]string
}

func ParseStatusEvent(typ EventCode, raw string) *StatusEvent {
	event := &StatusEvent{Raw: raw, Type: typ, Arguments: map[string]string{}}
	event.Severity, raw, _ = util.PartitionString(raw, ' ')
	var ok bool
	event.Action, raw, ok = util.PartitionString(raw, ' ')
	var attr string
	for ok {
		attr, raw, ok = util.PartitionString(raw, ' ')
		key, val, _ := util.PartitionString(attr, '=')
		event.Arguments[key], _ = util.UnescapeSimpleQuotedStringIfNeeded(val)
	}
	return event
}

func (s *StatusEvent) Code() EventCode { return s.Type }