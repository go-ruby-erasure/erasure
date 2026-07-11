// Package erasure is the pure-Go, Ruby-runtime-independent core of the Ruby
// `erasure` gem: a reflective adapter over the erasure-coding libraries
// github.com/go-erasure/reedsolomon (Reed-Solomon over GF(2^16)) and
// github.com/go-erasure/mojette (the Mojette / discrete-Radon transform code),
// shaped so that github.com/go-embedded-ruby/ruby can bind it as
// `require "erasure"`.
//
// A Session exposes both codecs through typed methods that return Ruby-shaped
// values — Arrays ([]any), Hashes (map[string]any) and scalars — plus a single
// dynamic entry point, Call, which maps a snake_case method name
// ("encode_rs", "reconstruct_rs", "verify_rs", "encode_mojette",
// "reconstruct_mojette", "reconstructible") to the matching Go call, coerces
// the Ruby-decoded arguments (ints, byte strings, Arrays), and returns the
// same Ruby-shaped values. Nothing here depends on the Ruby runtime, so the
// package is equally usable as a standalone Go library.
//
// Data shapes (as seen from Ruby):
//
//   - A shard set is an Array of byte strings: ["\x00..", "\x11.."].
//   - A "present" mask is an Array of booleans, one per shard.
//   - A Mojette grid is an Array of Rows*Cols byte strings (row-major blocks).
//   - A Mojette direction is a 2-element Array [p, q].
//   - A Mojette projection is a Hash {"p"=>Int, "q"=>Int, "bins"=>[byte, ...]}.
package erasure

import (
	"context"
	"fmt"

	"github.com/go-erasure/mojette"
	"github.com/go-erasure/reedsolomon"
)

// Session is a Ruby-facing handle over the erasure-coding libraries. It holds
// no mutable state; codecs are constructed per call from their parameters.
type Session struct{}

// Option configures a Session. It exists for parity with the other go-ruby
// satellite adapters and for forward compatibility; there are no options yet.
type Option func(*Session)

// NewSession builds a Session.
func NewSession(opts ...Option) *Session {
	s := &Session{}
	for _, o := range opts {
		o(s)
	}
	return s
}

// --- Reed-Solomon -----------------------------------------------------------

// EncodeRS encodes dataShards data shards into a full shard set. shards must
// hold exactly dataShards byte strings of equal, even length; parityShards
// parity shards are computed and appended. The returned Array holds the full
// data+parity shard set (dataShards+parityShards byte strings).
func (s *Session) EncodeRS(dataShards, parityShards int, shards [][]byte) ([]any, error) {
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	if len(shards) != dataShards {
		return nil, fmt.Errorf("erasure: encode_rs expects %d data shards, got %d", dataShards, len(shards))
	}
	shardLen := len(shards[0])
	full := make([][]byte, dataShards+parityShards)
	for i := 0; i < dataShards; i++ {
		full[i] = shards[i]
	}
	for i := dataShards; i < dataShards+parityShards; i++ {
		full[i] = make([]byte, shardLen)
	}
	if err := enc.Encode(full); err != nil {
		return nil, err
	}
	return bytesToArray(full), nil
}

// ReconstructRS rebuilds a full shard set from the surviving shards. shards and
// present must both have length dataShards+parityShards; present[i] reports
// whether shards[i] is intact. Missing shards may be nil or empty. The returned
// Array holds the recovered full shard set.
func (s *Session) ReconstructRS(dataShards, parityShards int, shards [][]byte, present []bool) ([]any, error) {
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	total := dataShards + parityShards
	if len(shards) != total {
		return nil, fmt.Errorf("erasure: reconstruct_rs expects %d shards, got %d", total, len(shards))
	}
	if len(present) != total {
		return nil, fmt.Errorf("erasure: reconstruct_rs expects %d present flags, got %d", total, len(present))
	}
	shardLen := 0
	for i, ok := range present {
		if ok && len(shards[i]) > 0 {
			shardLen = len(shards[i])
			break
		}
	}
	work := make([][]byte, total)
	for i := range shards {
		if present[i] {
			work[i] = shards[i]
		} else {
			work[i] = make([]byte, shardLen)
		}
	}
	if err := enc.Reconstruct(work, present); err != nil {
		return nil, err
	}
	return bytesToArray(work), nil
}

// VerifyRS reports whether the parity in a full shard set is consistent with
// its data. shards must hold dataShards+parityShards byte strings.
func (s *Session) VerifyRS(dataShards, parityShards int, shards [][]byte) (bool, error) {
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return false, err
	}
	return enc.Verify(shards)
}

// --- Mojette ----------------------------------------------------------------

// EncodeMojette projects a rows*cols grid of blockSize-byte blocks along the
// given directions. data holds rows*cols byte strings in row-major order; dirs
// is a list of [p, q] pairs. The returned Array holds one projection Hash per
// direction, each {"p"=>Int, "q"=>Int, "bins"=>Array of byte strings}.
func (s *Session) EncodeMojette(rows, cols, blockSize int, data [][]byte, dirs [][2]int) ([]any, error) {
	g := &mojette.Grid{Rows: rows, Cols: cols, BlockSize: blockSize, Data: data}
	projs, err := mojette.Encode(g, toDirections(dirs))
	if err != nil {
		return nil, err
	}
	return projectionsToArray(projs), nil
}

// ReconstructMojette rebuilds a rows*cols grid of blockSize-byte blocks from a
// Katz-sufficient set of projections. projs is an Array of projection Hashes as
// produced by EncodeMojette. The returned Array holds the rows*cols grid blocks
// in row-major order.
func (s *Session) ReconstructMojette(rows, cols, blockSize int, projs []any) ([]any, error) {
	ps, err := toProjections(projs)
	if err != nil {
		return nil, err
	}
	g, err := mojette.Reconstruct(rows, cols, blockSize, ps)
	if err != nil {
		return nil, err
	}
	return bytesToArray(g.Data), nil
}

// Reconstructible reports whether a rows*cols grid is recoverable from the
// projections along the given [p, q] directions (the Katz criterion).
func (s *Session) Reconstructible(rows, cols int, dirs [][2]int) bool {
	return mojette.Reconstructible(rows, cols, toDirections(dirs))
}

// --- Dynamic dispatch -------------------------------------------------------

// Call is the reflective dispatch entry point an rbgo binding drives from
// method_missing. It routes a snake_case method name to the matching Session
// operation, coercing the Ruby-decoded arguments (ints, byte strings, Arrays).
// It returns a clear error for unknown methods and for argument count or type
// mismatches.
func (s *Session) Call(_ context.Context, method string, args ...any) (any, error) {
	switch method {
	case "encode_rs":
		if err := wantArgs(method, args, 3); err != nil {
			return nil, err
		}
		d, e := argInt(method, args, 0)
		if e != nil {
			return nil, e
		}
		p, e := argInt(method, args, 1)
		if e != nil {
			return nil, e
		}
		sh, e := argShards(method, args, 2)
		if e != nil {
			return nil, e
		}
		return s.EncodeRS(d, p, sh)
	case "reconstruct_rs":
		if err := wantArgs(method, args, 4); err != nil {
			return nil, err
		}
		d, e := argInt(method, args, 0)
		if e != nil {
			return nil, e
		}
		p, e := argInt(method, args, 1)
		if e != nil {
			return nil, e
		}
		sh, e := argShards(method, args, 2)
		if e != nil {
			return nil, e
		}
		pr, e := argBools(method, args, 3)
		if e != nil {
			return nil, e
		}
		return s.ReconstructRS(d, p, sh, pr)
	case "verify_rs":
		if err := wantArgs(method, args, 3); err != nil {
			return nil, err
		}
		d, e := argInt(method, args, 0)
		if e != nil {
			return nil, e
		}
		p, e := argInt(method, args, 1)
		if e != nil {
			return nil, e
		}
		sh, e := argShards(method, args, 2)
		if e != nil {
			return nil, e
		}
		return s.VerifyRS(d, p, sh)
	case "encode_mojette":
		if err := wantArgs(method, args, 5); err != nil {
			return nil, err
		}
		r, e := argInt(method, args, 0)
		if e != nil {
			return nil, e
		}
		c, e := argInt(method, args, 1)
		if e != nil {
			return nil, e
		}
		bs, e := argInt(method, args, 2)
		if e != nil {
			return nil, e
		}
		data, e := argShards(method, args, 3)
		if e != nil {
			return nil, e
		}
		dirs, e := argDirs(method, args, 4)
		if e != nil {
			return nil, e
		}
		return s.EncodeMojette(r, c, bs, data, dirs)
	case "reconstruct_mojette":
		if err := wantArgs(method, args, 4); err != nil {
			return nil, err
		}
		r, e := argInt(method, args, 0)
		if e != nil {
			return nil, e
		}
		c, e := argInt(method, args, 1)
		if e != nil {
			return nil, e
		}
		bs, e := argInt(method, args, 2)
		if e != nil {
			return nil, e
		}
		projs, e := argArray(method, args, 3)
		if e != nil {
			return nil, e
		}
		return s.ReconstructMojette(r, c, bs, projs)
	case "reconstructible":
		if err := wantArgs(method, args, 3); err != nil {
			return nil, err
		}
		r, e := argInt(method, args, 0)
		if e != nil {
			return nil, e
		}
		c, e := argInt(method, args, 1)
		if e != nil {
			return nil, e
		}
		dirs, e := argDirs(method, args, 2)
		if e != nil {
			return nil, e
		}
		return s.Reconstructible(r, c, dirs), nil
	default:
		return nil, fmt.Errorf("erasure: unknown method %q", method)
	}
}

// --- Ruby value coercion ----------------------------------------------------

// bytesToArray turns a slice of byte strings into a Ruby Array ([]any).
func bytesToArray(bs [][]byte) []any {
	out := make([]any, len(bs))
	for i, b := range bs {
		out[i] = b
	}
	return out
}

// toDirections converts [p, q] pairs into mojette directions.
func toDirections(dirs [][2]int) []mojette.Direction {
	out := make([]mojette.Direction, len(dirs))
	for i, d := range dirs {
		out[i] = mojette.Direction{P: d[0], Q: d[1]}
	}
	return out
}

// projectionsToArray shapes mojette projections into Ruby Hashes.
func projectionsToArray(projs []mojette.Projection) []any {
	out := make([]any, len(projs))
	for i, p := range projs {
		out[i] = map[string]any{
			"p":    p.Dir.P,
			"q":    p.Dir.Q,
			"bins": bytesToArray(p.Bins),
		}
	}
	return out
}

// toProjections parses Ruby projection Hashes back into mojette projections.
func toProjections(projs []any) ([]mojette.Projection, error) {
	out := make([]mojette.Projection, len(projs))
	for i, v := range projs {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("erasure: projection %d is %T, want a Hash", i, v)
		}
		p, err := asInt(m["p"])
		if err != nil {
			return nil, fmt.Errorf("erasure: projection %d p: %w", i, err)
		}
		q, err := asInt(m["q"])
		if err != nil {
			return nil, fmt.Errorf("erasure: projection %d q: %w", i, err)
		}
		binsAny, ok := m["bins"].([]any)
		if !ok {
			return nil, fmt.Errorf("erasure: projection %d bins is %T, want an Array", i, m["bins"])
		}
		bins, err := asShards(binsAny)
		if err != nil {
			return nil, fmt.Errorf("erasure: projection %d bins: %w", i, err)
		}
		out[i] = mojette.Projection{Dir: mojette.Direction{P: p, Q: q}, Bins: bins}
	}
	return out, nil
}

// asInt coerces a Ruby-decoded numeric value to an int.
func asInt(v any) (int, error) {
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		return int(t), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %T", v)
	}
}

// asBytes coerces a Ruby-decoded byte string or String to a []byte.
func asBytes(v any) ([]byte, error) {
	switch t := v.(type) {
	case []byte:
		return t, nil
	case string:
		return []byte(t), nil
	default:
		return nil, fmt.Errorf("expected a byte string, got %T", v)
	}
}

// asShards coerces a Ruby Array of byte strings to a [][]byte.
func asShards(v []any) ([][]byte, error) {
	out := make([][]byte, len(v))
	for i, e := range v {
		b, err := asBytes(e)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out[i] = b
	}
	return out, nil
}

// asBools coerces a Ruby Array of booleans to a []bool.
func asBools(v []any) ([]bool, error) {
	out := make([]bool, len(v))
	for i, e := range v {
		b, ok := e.(bool)
		if !ok {
			return nil, fmt.Errorf("element %d: expected a boolean, got %T", i, e)
		}
		out[i] = b
	}
	return out, nil
}

// asDirs coerces a Ruby Array of [p, q] pairs to a [][2]int.
func asDirs(v []any) ([][2]int, error) {
	out := make([][2]int, len(v))
	for i, e := range v {
		pair, ok := e.([]any)
		if !ok || len(pair) != 2 {
			return nil, fmt.Errorf("direction %d: expected a [p, q] pair, got %T", i, e)
		}
		p, err := asInt(pair[0])
		if err != nil {
			return nil, fmt.Errorf("direction %d p: %w", i, err)
		}
		q, err := asInt(pair[1])
		if err != nil {
			return nil, fmt.Errorf("direction %d q: %w", i, err)
		}
		out[i] = [2]int{p, q}
	}
	return out, nil
}

// wantArgs checks the argument count for a dispatched method.
func wantArgs(method string, args []any, n int) error {
	if len(args) != n {
		return fmt.Errorf("erasure: %s expects %d arguments, got %d", method, n, len(args))
	}
	return nil
}

// argInt reads args[i] as an int, wrapping coercion errors with context.
func argInt(method string, args []any, i int) (int, error) {
	n, err := asInt(args[i])
	if err != nil {
		return 0, fmt.Errorf("erasure: %s arg %d: %w", method, i, err)
	}
	return n, nil
}

// argArray reads args[i] as a Ruby Array.
func argArray(method string, args []any, i int) ([]any, error) {
	a, ok := args[i].([]any)
	if !ok {
		return nil, fmt.Errorf("erasure: %s arg %d: expected an Array, got %T", method, i, args[i])
	}
	return a, nil
}

// argShards reads args[i] as an Array of byte strings.
func argShards(method string, args []any, i int) ([][]byte, error) {
	a, err := argArray(method, args, i)
	if err != nil {
		return nil, err
	}
	sh, err := asShards(a)
	if err != nil {
		return nil, fmt.Errorf("erasure: %s arg %d: %w", method, i, err)
	}
	return sh, nil
}

// argBools reads args[i] as an Array of booleans.
func argBools(method string, args []any, i int) ([]bool, error) {
	a, err := argArray(method, args, i)
	if err != nil {
		return nil, err
	}
	b, err := asBools(a)
	if err != nil {
		return nil, fmt.Errorf("erasure: %s arg %d: %w", method, i, err)
	}
	return b, nil
}

// argDirs reads args[i] as an Array of [p, q] pairs.
func argDirs(method string, args []any, i int) ([][2]int, error) {
	a, err := argArray(method, args, i)
	if err != nil {
		return nil, err
	}
	d, err := asDirs(a)
	if err != nil {
		return nil, fmt.Errorf("erasure: %s arg %d: %w", method, i, err)
	}
	return d, nil
}
