# gojq [![CI Status](https://github.com/itchyny/gojq/workflows/CI/badge.svg)](https://github.com/itchyny/gojq/actions)
Pure Go implementation of [jq](https://github.com/stedolan/jq).

## Usage
```sh
 $ echo '{"foo": 128}' | gojq '.foo'
128
 $ echo '{"a": {"b": 42}}' | gojq '.a.b'
42
 $ echo '{"id": "sample", "10": {"b": 42}}' | gojq '{(.id): .["10"].b}'
{
  "sample": 42
}
 $ echo '[{"id":1},{"id":2},{"id":3}]' | gojq '.[] | .id'
1
2
3
 $ echo '{"a":1,"b":2}' | gojq '.a += 1 | .b *= 2'
{
  "a": 2,
  "b": 4
}
 $ echo '{"a":1} [2] 3' | gojq '. as {$a} ?// [$a] ?// $a | $a'
1
2
3
 $ echo '{"foo": 4722366482869645213696}' | gojq .foo
4722366482869645213696  # keeps the precision of number while jq does not
 $ gojq -n 'def fact($n): if $n < 1 then 1 else $n * fact($n - 1) end; fact(50)'
30414093201713378043612608166064768844377641568960512000000000000 # arbitrary-precision integer calculation
```

Nice error messages.
```sh
 $ echo '[1,2,3]' | gojq  '.foo & .bar'
gojq: invalid query: .foo & .bar
    .foo & .bar
         ^  unexpected token "&"
 $ echo '{"foo": { bar: [] } }' | gojq '.'
gojq: invalid json: <stdin>
    {"foo": { bar: [] } }
              ^  invalid character 'b' looking for beginning of object key string
```

## As-of join
`asof_join` is a point-in-time association of events with the version of a
dimension which was effective at the time of the event. It is useful when a
dimension changes at 10:00 but an event which happened at 9:00 arrives late:
the event is still associated with the old version.

```sh
 $ echo '[{"device":"d1","ts":9,"event":"boot"}]' \
   | gojq -c 'asof_join([
       {"device":"d1","effective":0,"id":"v1","mode":"old"},
       {"device":"d1","effective":10,"id":"v2","mode":"new"}
     ]; {"key":"device","event_time":"ts","version_time":"effective"})'
[{"event":{"device":"d1","event":"boot","ts":9},"version":{"device":"d1","effective":0,"id":"v1","mode":"old"},"version_id":"v1","version_index":0}]
```

The input is the array of events and the argument is the array of version
changes, both sorted by the composite key and then by time. Every event selects
the version whose time is not later than the event time, and the result carries
the version identity (`version_id`), the full version (`version`), and the
position of the version in the versions array (`version_index`). Events without
any version at or before their time are kept with `null` versions.

- The default field names are `key`, `time`, and `id`. A composite key is an
  array in the `key` field, or a list of field names in the `key` option.
- Version changes at the same key and time conflict. The change with the
  highest `priority` field wins, and a tie is resolved by the later record in
  the versions array. Version changes at exactly the event time are visible to
  the event.
- The events and the versions must be sorted by the composite key and then by
  time. Violating this precondition raises an error instead of silently
  associating wrong versions. The join is a single forward merge, so the
  version history is never rescanned for each event.
- `asof_join_stream` accepts a single interleaved array of events and version
  changes tagged by a `kind` field (`"event"` or `"version"`), sorted by key
  and time.

The optional options object supports `key`, `event_key`, `version_key`, `time`,
`event_time`, `version_time`, `id`, `priority`, `default_priority`, `kind`,
`event`, and `version`.

## Installation
### Homebrew
```sh
brew install itchyny/tap/gojq
```

### Build from source
```bash
go get -u github.com/itchyny/gojq/cmd/gojq
```

## Difference to jq
- gojq is purely implemented with Go language and is completely portable. jq depends on the C standard library so the availability of math functions depends on the library. jq also depends on the regular expression library and it makes build scripts complex.
- gojq implements nice error messages for invalid query and JSON input. The error message of jq is sometimes difficult to tell where to fix the query.
- gojq does not keep the order of object keys. I understand this might cause problems for some scripts but basically we should not rely on the order of object keys. I would implement when ordered map is implemented in the standard library of Go but I'm less motivated.
- gojq supports arbitrary-precision integer calculation while jq does not. This is important to keeping the precision of numeric IDs or nanosecond values. You can use gojq to solve some mathematical problems which require big integers.
- gojq supports reading from YAML input while jq does not. gojq also supports YAML output.

## Usage as a library
You can use the gojq parser and interpreter from your Go products.

```go
package main

import (
	"fmt"
	"log"

	"github.com/itchyny/gojq"
)

func main() {
	query, err := gojq.Parse(".foo | ..")
	if err != nil {
		log.Fatalln(err)
	}
	input := map[string]interface{}{"foo": []interface{}{1, 2, 3}}
	iter := query.Run(input) // or query.RunWithContext
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			log.Fatalln(err)
		}
		fmt.Printf("%#v\n", v)
	}
}
```

- Firstly, use [`gojq.Parse(string) (*Query, error)`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Parse) to get the query from a string.
- Secondly, get the result iterator
  - using [`query.Run`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Query.Run) or [`query.RunWithContext`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Query.RunWithContext)
  - or alternatively, compile the query using [`gojq.Compile`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Compile) and then [`code.Run`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Code.Run) or [`code.RunWithContext`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Code.RunWithContext). You can reuse the `*Code` against multiple inputs to avoid compiling the same query.
  - In either case, the query input should have type `[]interface{}` for an array and `map[string]interface{}` for a map (just like decoded to an `interface{}` using the [encoding/json](https://golang.org/pkg/encoding/json/) package). You can't use `[]int` or `map[string]string`, for example.
- Thirdly, iterate through the results using [`iter.Next() (interface{}, bool)`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Iter). The iterater can emit an error so make sure to handle it. Termination is notified by the second returned value of `Next()`.

`gojq.Compile` allows to configure the following compiler options. You can look into [examples](./option_test.go).

- [`gojq.WithModuleLoader`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#WithModuleLoader) allows to load modules. By default, the module feature is disabled.
- [`gojq.WithEnvironLoader`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#WithEnvironLoader) allows to configure the environment variables referenced by `env` and `$ENV`. By default, OS environment variables are not accessible due to security reason. You can use `gojq.WithEnvironLoader(os.Environ)` if you want.
- [`gojq.WithVariables`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#WithVariables) allows to configure the variables which can be used in the query. Pass the values of the variables to [`code.Run`](https://pkg.go.dev/github.com/itchyny/gojq?tab=doc#Code.Run) in the same order.

## Bug Tracker
Report bug at [Issues・itchyny/gojq - GitHub](https://github.com/itchyny/gojq/issues).

## Author
itchyny (https://github.com/itchyny)

## License
This software is released under the MIT License, see LICENSE.
