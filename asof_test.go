package gojq_test

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"testing"

	"github.com/itchyny/gojq"
)

func TestAsofJoin(t *testing.T) {
	testCases := []struct {
		query  string
		input  string
		output string
	}{
		{
			query: `asof_join([
				{"key": "d1", "time": 0, "id": "v1"},
				{"key": "d1", "time": 10, "id": "v2"}
			]) | map(.version_id)`,
			input:  `[{"key":"d1","time":5},{"key":"d1","time":9},{"key":"d1","time":10},{"key":"d1","time":15}]`,
			output: `["v1","v1","v2","v2"]`,
		},
		{
			// An event with no version of its key is kept with a null version.
			query:  `asof_join([{"key":"d2","time":0,"id":"v2"}]) | map(.version_id)`,
			input:  `[{"key":"d1","time":9},{"key":"d2","time":9}]`,
			output: `[null,"v2"]`,
		},
		{
			// Versions at exactly the event time are visible to the event.
			query:  `asof_join([{"key":"d1","time":10,"id":"v1"}]) | map(.version_id)`,
			input:  `[{"key":"d1","time":10}]`,
			output: `["v1"]`,
		},
		{
			// Same-instant conflicts are resolved by the declared priority.
			query: `asof_join([
				{"key":"d1","time":10,"id":"low","priority":1},
				{"key":"d1","time":10,"id":"high","priority":9}
			]) | map(.version_id)`,
			input:  `[{"key":"d1","time":10}]`,
			output: `["high"]`,
		},
		{
			// Same-instant conflicts with equal priorities keep the later input.
			query: `asof_join([
				{"key":"d1","time":10,"id":"v1","priority":9},
				{"key":"d1","time":10,"id":"v2","priority":9}
			]) | map(.version_id)`,
			input:  `[{"key":"d1","time":10}]`,
			output: `["v2"]`,
		},
		{
			// The version state of a key does not leak into another key.
			query: `asof_join([
				{"key":["a",1],"time":0,"id":"a0"},
				{"key":["a",2],"time":3,"id":"b3"}
			]) | map(.version_id)`,
			input:  `[{"key":["a",1],"time":2},{"key":["a",2],"time":1},{"key":["a",2],"time":3}]`,
			output: `["a0",null,"b3"]`,
		},
		{
			// A custom key, time and identity field names.
			query: `asof_join([
				{"device":"d1","effective":0,"rev":"r1"},
				{"device":"d1","effective":10,"rev":"r2"}
			]; {"key":"device","event_time":"ts","version_time":"effective","id":"rev"})
			| map(.version_id)`,
			input:  `[{"device":"d1","ts":9},{"device":"d1","ts":10}]`,
			output: `["r1","r2"]`,
		},
		{
			// Version identity falls back to a custom priority field.
			query: `asof_join([
				{"key":"d1","time":10,"id":"v1","p":1},
				{"key":"d1","time":10,"id":"v2","p":9}
			]; {"priority":"p"}) | map(.version_id)`,
			input:  `[{"key":"d1","time":10}]`,
			output: `["v2"]`,
		},
		{
			// The interleaved form accepts a single tagged record stream.
			query: `asof_join_stream | map(.version_id)`,
			input: `[
				{"kind":"version","key":"a","time":0,"id":"a0"},
				{"kind":"event","key":"a","time":9,"id":"e1"},
				{"kind":"version","key":"a","time":10,"id":"a10"}
			]`,
			output: `["a0"]`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.query, func(t *testing.T) {
			query, err := gojq.Parse(tc.query)
			if err != nil {
				t.Fatalf("invalid query: %v", err)
			}
			code, err := gojq.Compile(query)
			if err != nil {
				t.Fatalf("invalid query: %v", err)
			}
			var input interface{}
			if err := json.Unmarshal([]byte(tc.input), &input); err != nil {
				t.Fatalf("invalid input: %v", err)
			}
			var output []interface{}
			iter := code.Run(input)
			for {
				v, ok := iter.Next()
				if !ok {
					break
				}
				if err, ok := v.(error); ok {
					t.Fatalf("error: %v", err)
				}
				output = append(output, v)
			}
			var expected interface{}
			if err := json.Unmarshal([]byte(tc.output), &expected); err != nil {
				t.Fatalf("invalid expected output: %v", err)
			}
			if !reflect.DeepEqual(output, []interface{}{expected}) {
				t.Errorf("expected: %v, got: %v", expected, output)
			}
		})
	}
}

func TestAsofJoinOrderError(t *testing.T) {
	query, err := gojq.Parse(`asof_join([])`)
	if err != nil {
		log.Fatalln(err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		log.Fatalln(err)
	}
	iter := code.Run([]interface{}{
		map[string]interface{}{"key": "d", "time": 10},
		map[string]interface{}{"key": "d", "time": 5},
	})
	v, ok := iter.Next()
	if !ok {
		t.Fatal("expected an error")
	}
	expected := `asof_join: events are not sorted by key and time: ` +
		`events[1] (key "d", time 5) sorts before events[0] (key "d", time 10)`
	if err, ok := v.(error); !ok || err.Error() != expected {
		t.Errorf("expected: %v, got: %v", expected, v)
	}
}

func Example_asofJoin() {
	query, err := gojq.Parse(`asof_join([
		{"key":"d1","time":0,"id":"v1"},
		{"key":"d1","time":10,"id":"v2"}
	]) | map(.event.id + ":" + .version_id)`)
	if err != nil {
		log.Fatalln(err)
	}
	iter := query.Run([]interface{}{
		map[string]interface{}{"key": "d1", "time": 9, "id": "e1"},
		map[string]interface{}{"key": "d1", "time": 10, "id": "e2"},
	})
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			log.Fatalln(err)
		}
		fmt.Printf("%v\n", v)
	}
	// Output:
	// [e1:v1 e2:v2]
}
