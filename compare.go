package gojq

import (
	"math"
	"math/big"
)

// twoToThe63 is 2^63 as an exact float64 value; any int64 value i satisfies
// -twoToThe63 <= i < twoToThe63.
const twoToThe63 = 1 << 63

func compare(l, r interface{}) int {
	switch l := l.(type) {
	case int:
		switch r := r.(type) {
		case int:
			switch {
			case l < r:
				return -1
			case l == r:
				return 0
			default:
				return 1
			}
		case float64:
			return compareIntFloat(l, r)
		case *big.Int:
			return big.NewInt(int64(l)).Cmp(r)
		}
	case float64:
		switch r := r.(type) {
		case int:
			return -compareIntFloat(r, l)
		case float64:
			return compareFloats(l, r)
		case *big.Int:
			return -compareBigIntFloat(r, l)
		}
	case *big.Int:
		switch r := r.(type) {
		case int:
			return l.Cmp(big.NewInt(int64(r)))
		case float64:
			return compareBigIntFloat(l, r)
		case *big.Int:
			return l.Cmp(r)
		}
	case string:
		if r, ok := r.(string); ok {
			switch {
			case l < r:
				return -1
			case l == r:
				return 0
			default:
				return 1
			}
		}
	case []interface{}:
		if r, ok := r.([]interface{}); ok {
			for i := 0; ; i++ {
				if i >= len(l) {
					if i >= len(r) {
						return 0
					}
					return -1
				}
				if i >= len(r) {
					return 1
				}
				if cmp := compare(l[i], r[i]); cmp != 0 {
					return cmp
				}
			}
		}
	case map[string]interface{}:
		if r, ok := r.(map[string]interface{}); ok {
			lk, rk := funcKeys(l), funcKeys(r)
			if cmp := compare(lk, rk); cmp != 0 {
				return cmp
			}
			for _, k := range lk.([]interface{}) {
				if cmp := compare(l[k.(string)], r[k.(string)]); cmp != 0 {
					return cmp
				}
			}
			return 0
		}
	}
	return compareTypeOrder(l, r)
}

// compareFloats compares two float64 values by their exact binary values.
// Non-finite values are ordered as -Infinity < finite numbers < Infinity <
// NaN, NaN is equal to itself, and negative zero is equal to positive zero.
func compareFloats(l, r float64) int {
	switch {
	case l < r:
		return -1
	case l > r:
		return 1
	case l == r:
		return 0
	case math.IsNaN(l):
		if math.IsNaN(r) {
			return 0
		}
		return 1
	default:
		return -1
	}
}

// compareIntFloat compares an int with a float64 value exactly, without
// rounding the integer to a float64. Non-finite values are ordered as in
// compareFloats.
func compareIntFloat(l int, r float64) int {
	switch {
	case math.IsNaN(r):
		return -1
	case math.IsInf(r, 1):
		return -1
	case math.IsInf(r, -1):
		return 1
	}
	t := math.Trunc(r)
	switch {
	case t >= twoToThe63:
		return -1
	case t < -twoToThe63:
		return 1
	}
	ti := int64(t)
	switch {
	case int64(l) < ti:
		return -1
	case int64(l) > ti:
		return 1
	case r > t: // r has a positive fractional part, so l < r
		return -1
	case r < t: // r has a negative fractional part, so l > r
		return 1
	default:
		return 0
	}
}

// compareBigIntFloat compares a *big.Int with a float64 value exactly,
// without rounding the integer to a float64. Non-finite values are ordered
// as in compareFloats.
func compareBigIntFloat(l *big.Int, r float64) int {
	switch {
	case math.IsNaN(r):
		return -1
	case math.IsInf(r, 1):
		return -1
	case math.IsInf(r, -1):
		return 1
	}
	// Both conversions are exact; the integer keeps its full precision, and
	// any float64 value is exactly representable with 53-bit precision.
	return new(big.Float).SetInt(l).Cmp(new(big.Float).SetFloat64(r))
}

func compareTypeOrder(l, r interface{}) int {
	ln, rn := getTypeOrdNum(l), getTypeOrdNum(r)
	switch {
	case ln < rn:
		return -1
	case ln == rn:
		return 0
	default:
		return 1
	}
}

func getTypeOrdNum(v interface{}) int {
	switch v := v.(type) {
	case nil:
		return 0
	case bool:
		if v {
			return 2
		}
		return 1
	case int, float64, *big.Int:
		return 3
	case string:
		return 4
	case []interface{}:
		return 5
	case map[string]interface{}:
		return 6
	default:
		return -1
	}
}
