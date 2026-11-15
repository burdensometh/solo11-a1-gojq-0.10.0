package gojq

import (
	"fmt"
	"math/big"
)

// The as-of join ("point-in-time" join) implemented in this file backs the
// asof_join and asof_join_stream builtin functions defined in builtin.jq.
//
// The join associates every event with the most recent version of its
// dimension which is effective at the time of the event, that is the version
// whose time is not later than the event time. Version changes which happen
// exactly at the event time are visible to the event, so an event at 10:00
// sees the version which became effective at 10:00. An event older than every
// version of its key, or an event whose key has no version at all, is kept
// with a null version instead of being dropped.
//
// The events and the versions have to be sorted by the composite key and then
// by time. Violating this precondition is reported as an error instead of
// silently producing wrong associations. The inputs are consumed by a merge
// which only moves its cursors forward, so the join is linear in the number
// of records and the version history is never rescanned per event.

// asofOptions is the parsed form of the optional options object accepted by
// the as-of join builtins. A key or time specification is a field name, or an
// array of field names for a composite key.
type asofOptions struct {
	eventKey        []interface{}
	versionKey      []interface{}
	eventTime       []interface{}
	versionTime     []interface{}
	versionID       interface{}
	versionPriority interface{}
	defaultPriority interface{}
	kindField       interface{}
	eventKind       interface{}
	versionKind     interface{}
}

func newAsofOptions(v interface{}) (*asofOptions, interface{}) {
	opts := &asofOptions{
		eventKey:        []interface{}{"key"},
		versionKey:      []interface{}{"key"},
		eventTime:       []interface{}{"time"},
		versionTime:     []interface{}{"time"},
		versionID:       "id",
		versionPriority: "priority",
		defaultPriority: 0,
		kindField:       "kind",
		eventKind:       "event",
		versionKind:     "version",
	}
	if v == nil {
		return opts, nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("asof_join: options must be an object or null: %s", typeErrorPreview(v))
	}
	for name := range m {
		switch name {
		case "key", "event_key", "version_key", "time", "event_time",
			"version_time", "id", "priority", "default_priority", "kind",
			"event", "version":
		default:
			return nil, fmt.Errorf("asof_join: unknown option %q", name)
		}
	}
	// Apply the options in a fixed order independent of map iteration, so
	// that the specific options override the shared shorthand options.
	for _, name := range []string{"key", "event_key", "version_key"} {
		x, ok := m[name]
		if !ok {
			continue
		}
		fields, err := asofFields(name, x)
		if err != nil {
			return nil, err
		}
		switch name {
		case "key":
			opts.eventKey, opts.versionKey = fields, fields
		case "event_key":
			opts.eventKey = fields
		case "version_key":
			opts.versionKey = fields
		}
	}
	for _, name := range []string{"time", "event_time", "version_time"} {
		x, ok := m[name]
		if !ok {
			continue
		}
		fields, err := asofFields(name, x)
		if err != nil {
			return nil, err
		}
		switch name {
		case "time":
			opts.eventTime, opts.versionTime = fields, fields
		case "event_time":
			opts.eventTime = fields
		case "version_time":
			opts.versionTime = fields
		}
	}
	for _, name := range []string{"id", "priority", "kind", "event", "version"} {
		x, ok := m[name]
		if !ok {
			continue
		}
		field, err := asofOptionalField(name, x)
		if err != nil {
			return nil, err
		}
		switch name {
		case "id":
			opts.versionID = field
		case "priority":
			opts.versionPriority = field
		case "kind":
			opts.kindField = field
		case "event":
			opts.eventKind = field
		case "version":
			opts.versionKind = field
		}
	}
	if x, ok := m["default_priority"]; ok {
		if typeof(x) != "number" {
			return nil, fmt.Errorf("asof_join: option %q must be a number: %s", "default_priority", typeErrorPreview(x))
		}
		opts.defaultPriority = x
	}
	return opts, nil
}

func asofFields(name string, x interface{}) ([]interface{}, interface{}) {
	switch x := x.(type) {
	case string:
		return []interface{}{x}, nil
	case []interface{}:
		if len(x) == 0 {
			return nil, fmt.Errorf("asof_join: option %q must not be an empty array", name)
		}
		for _, y := range x {
			if _, ok := y.(string); !ok {
				return nil, fmt.Errorf("asof_join: option %q must be a string or an array of strings: %s", name, typeErrorPreview(x))
			}
		}
		return x, nil
	default:
		return nil, fmt.Errorf("asof_join: option %q must be a string or an array of strings: %s", name, typeErrorPreview(x))
	}
}

func asofOptionalField(name string, x interface{}) (interface{}, interface{}) {
	switch x := x.(type) {
	case nil:
		return nil, nil
	case string:
		return x, nil
	default:
		return nil, fmt.Errorf("asof_join: option %q must be a string or null: %s", name, typeErrorPreview(x))
	}
}

// asofSequence is a sequence of records together with the extracted composite
// keys and times of the records.
type asofSequence struct {
	records []interface{}
	keys    []interface{}
	times   []interface{}
}

func (seq *asofSequence) append(x, key, time interface{}) {
	seq.records = append(seq.records, x)
	seq.keys = append(seq.keys, key)
	seq.times = append(seq.times, time)
}

// funcAsofJoin is the internal implementation of asof_join. The input value is
// the array of events, the first argument is the array of versions sorted by
// key and time, and the second argument is the options object (or null).
func funcAsofJoin(v interface{}, args []interface{}) interface{} {
	var versions interface{}
	if len(args) > 0 {
		versions = args[0]
	}
	var rawOptions interface{}
	if len(args) > 1 {
		rawOptions = args[1]
	}
	opts, err := newAsofOptions(rawOptions)
	if err != nil {
		return err
	}
	events, err := asofSequenceOf("events", v, opts.eventKey, opts.eventTime)
	if err != nil {
		return err
	}
	changes, err := asofSequenceOf("versions", versions, opts.versionKey, opts.versionTime)
	if err != nil {
		return err
	}
	if err := asofCheckSorted("asof_join", "events", events); err != nil {
		return err
	}
	if err := asofCheckSorted("asof_join", "versions", changes); err != nil {
		return err
	}
	return asofJoin("asof_join", events, changes, opts)
}

// funcAsofJoinStream is the internal implementation of asof_join_stream. The
// input value is a single array of events and version changes tagged by the
// kind field, sorted by key and time.
func funcAsofJoinStream(v interface{}, args []interface{}) interface{} {
	var rawOptions interface{}
	if len(args) > 0 {
		rawOptions = args[0]
	}
	opts, err := newAsofOptions(rawOptions)
	if err != nil {
		return err
	}
	xs, ok := v.([]interface{})
	if !ok {
		return fmt.Errorf("asof_join_stream: input must be an array: %s", typeErrorPreview(v))
	}
	if opts.kindField == nil || opts.eventKind == nil || opts.versionKind == nil {
		return fmt.Errorf("asof_join_stream: the kind, event and version options must not be null")
	}
	events, changes := &asofSequence{}, &asofSequence{}
	all := &asofSequence{}
	for i, x := range xs {
		m, ok := x.(map[string]interface{})
		if !ok {
			return fmt.Errorf("asof_join_stream: records[%d] is not an object: %s", i, typeErrorPreview(x))
		}
		var keyFields, timeFields []interface{}
		var seq *asofSequence
		switch kind := m[opts.kindField.(string)]; {
		case asofEqual(kind, opts.eventKind):
			keyFields, timeFields, seq = opts.eventKey, opts.eventTime, events
		case asofEqual(kind, opts.versionKind):
			keyFields, timeFields, seq = opts.versionKey, opts.versionTime, changes
		default:
			return fmt.Errorf("asof_join_stream: records[%d].%s is not %s or %s: %s",
				i, opts.kindField, previewValue(opts.eventKind), previewValue(opts.versionKind), typeErrorPreview(kind))
		}
		key, time, err := asofRecordValues("asof_join_stream", fmt.Sprintf("records[%d]", i), x, keyFields, timeFields)
		if err != nil {
			return err
		}
		seq.append(x, key, time)
		all.append(x, key, time)
	}
	if err := asofCheckSorted("asof_join_stream", "records", all); err != nil {
		return err
	}
	return asofJoin("asof_join_stream", events, changes, opts)
}

func asofEqual(x, y interface{}) bool {
	return compare(x, y) == 0 && typeof(x) == typeof(y)
}

func asofSequenceOf(name string, v interface{}, keyFields, timeFields []interface{}) (*asofSequence, interface{}) {
	xs, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("asof_join: %s must be an array: %s", name, typeErrorPreview(v))
	}
	seq := &asofSequence{records: xs}
	for i, x := range xs {
		if _, ok := x.(map[string]interface{}); !ok {
			return nil, fmt.Errorf("asof_join: %s[%d] is not an object: %s", name, i, typeErrorPreview(x))
		}
		key, time, err := asofRecordValues("asof_join", fmt.Sprintf("%s[%d]", name, i), x, keyFields, timeFields)
		if err != nil {
			return nil, err
		}
		seq.keys, seq.times = append(seq.keys, key), append(seq.times, time)
	}
	return seq, nil
}

// asofRecordValues extracts the composite key and the time of a record. A
// missing key field is represented by null; a time has to be a number or a
// string so that the ordering precondition is meaningful.
func asofRecordValues(fn, name string, x interface{}, keyFields, timeFields []interface{}) (interface{}, interface{}, interface{}) {
	key := asofValue(x, keyFields)
	time := asofValue(x, timeFields)
	switch time.(type) {
	case int, float64, *big.Int, string:
	default:
		return nil, nil, fmt.Errorf("%s: %s time is not a number or a string: %s", fn, name, typeErrorPreview(time))
	}
	return key, time, nil
}

// asofValue returns the value of a single field of the record, or an array of
// the values of the fields when the key is composite.
func asofValue(v interface{}, fields []interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	if len(fields) == 1 {
		return m[fields[0].(string)]
	}
	xs := make([]interface{}, len(fields))
	for i, field := range fields {
		xs[i] = m[field.(string)]
	}
	return xs
}

// asofCheckSorted reports the first record which breaks the precondition that
// the records are sorted by the composite key and then by time.
func asofCheckSorted(fn, name string, seq *asofSequence) interface{} {
	for i := 1; i < len(seq.records); i++ {
		cmp := compare(seq.keys[i], seq.keys[i-1])
		if cmp < 0 || cmp == 0 && compare(seq.times[i], seq.times[i-1]) < 0 {
			return &asofJoinOrderError{
				fn: fn, name: name, index: i,
				key: seq.keys[i], time: seq.times[i],
				prevKey: seq.keys[i-1], prevTime: seq.times[i-1],
			}
		}
	}
	return nil
}

func asofVersionIDs(versions []interface{}, field interface{}) []interface{} {
	ids := make([]interface{}, len(versions))
	if field == nil {
		return ids
	}
	for i, version := range versions {
		if m, ok := version.(map[string]interface{}); ok {
			ids[i] = m[field.(string)]
		}
	}
	return ids
}

func asofVersionPriorities(fn string, versions []interface{}, field, defaultPriority interface{}) ([]interface{}, interface{}) {
	priorities := make([]interface{}, len(versions))
	for i, version := range versions {
		if field == nil {
			priorities[i] = defaultPriority
			continue
		}
		m, ok := version.(map[string]interface{})
		if !ok {
			priorities[i] = defaultPriority
			continue
		}
		p := m[field.(string)]
		if p == nil {
			priorities[i] = defaultPriority
			continue
		}
		if typeof(p) != "number" {
			return nil, fmt.Errorf("%s: versions[%d].%s is not a number: %s", fn, i, field, typeErrorPreview(p))
		}
		priorities[i] = p
	}
	return priorities, nil
}

// asofJoin performs the single-pass merge of the sorted events and versions.
//
// The version cursor only moves forward. Versions whose key is smaller than
// the event key are skipped, and versions of the event key are consumed as
// long as their time is not later than the event time. A run of versions
// sharing the same key and time is a same-instant conflict, and the version
// with the highest declared priority wins; a tie is resolved by the later
// position in the versions array. Consuming the whole run at once makes sure
// an event at that instant is associated with the winning version rather than
// with whichever conflicting version happens to come first.
func asofJoin(fn string, events, versions *asofSequence, opts *asofOptions) interface{} {
	ids := asofVersionIDs(versions.records, opts.versionID)
	priorities, err := asofVersionPriorities(fn, versions.records, opts.versionPriority, opts.defaultPriority)
	if err != nil {
		return err
	}
	results := make([]interface{}, len(events.records))
	vi := 0
	var version, id interface{}
	var versionIndex int
	var currentKey interface{}
	hasVersion := false
	for i, event := range events.records {
		key, eventTime := events.keys[i], events.times[i]
		if hasVersion && compare(currentKey, key) != 0 {
			hasVersion = false
		}
		for vi < len(versions.records) {
			versionKey, versionTime := versions.keys[vi], versions.times[vi]
			cmp := compare(versionKey, key)
			if cmp > 0 || cmp == 0 && compare(versionTime, eventTime) > 0 {
				break
			}
			// Consume the run of versions sharing this key and time, choosing
			// the highest priority, and the later one on a tie.
			winner := vi
			for j := vi + 1; j < len(versions.records) &&
				compare(versions.keys[j], versionKey) == 0 &&
				compare(versions.times[j], versionTime) == 0; j++ {
				if compare(priorities[j], priorities[winner]) >= 0 {
					winner = j
				}
				vi = j
			}
			version, id, versionIndex = versions.records[winner], ids[winner], winner
			currentKey, hasVersion = versionKey, true
			vi++
		}
		var matchedVersion, matchedID interface{}
		var matchedIndex interface{}
		if hasVersion && compare(currentKey, key) == 0 {
			matchedVersion, matchedID, matchedIndex = version, id, versionIndex
		}
		results[i] = map[string]interface{}{
			"event":         event,
			"version":       matchedVersion,
			"version_index": matchedIndex,
			"version_id":    matchedID,
		}
	}
	return results
}
