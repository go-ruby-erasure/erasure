# erasure

[![ci](https://github.com/go-ruby-erasure/erasure/actions/workflows/ci.yml/badge.svg)](https://github.com/go-ruby-erasure/erasure/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-ruby-erasure/erasure.svg)](https://pkg.go.dev/github.com/go-ruby-erasure/erasure)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

The pure-Go, Ruby-runtime-independent core of the Ruby `erasure` gem: a
reflective adapter over two erasure-coding libraries —
[`go-erasure/reedsolomon`](https://github.com/go-erasure/reedsolomon)
(Reed-Solomon over GF(2^16)) and
[`go-erasure/mojette`](https://github.com/go-erasure/mojette)
(the Mojette / discrete-Radon transform code) — shaped so that the
[go-embedded-ruby](https://github.com/go-embedded-ruby) interpreter can bind it
as `require "erasure"`.

A `Session` exposes both codecs through typed methods that return Ruby-shaped
values, plus a single dynamic entry point, `Call`, that a Ruby binding drives
from `method_missing`. Nothing here depends on the Ruby runtime, so the package
is equally usable as a standalone Go library. It is `CGO_ENABLED=0` and builds
for all nine supported 64-bit targets.

## Install

```sh
go get github.com/go-ruby-erasure/erasure
```

## Ruby data shapes

| Concept        | Ruby shape                                                     |
| -------------- | -------------------------------------------------------------- |
| shard / block  | a byte string (`[]byte`)                                       |
| shard set      | an `Array` of byte strings                                     |
| present mask   | an `Array` of booleans, one per shard                          |
| grid           | an `Array` of `rows*cols` byte strings (row-major blocks)      |
| direction      | a 2-element `Array` `[p, q]`                                   |
| projection     | a `Hash` `{"p" => Int, "q" => Int, "bins" => [byte, ...]}`     |

## API

```go
s := erasure.NewSession()

// Reed-Solomon.
full, _ := s.EncodeRS(2, 2, dataShards)              // []any of data+parity shards
ok,   _ := s.VerifyRS(2, 2, full)                    // bool
rec,  _ := s.ReconstructRS(2, 2, damaged, present)   // []any, recovered set

// Mojette.
projs, _ := s.EncodeMojette(2, 2, 2, gridBlocks, dirs) // []any of {"p","q","bins"}
grid,  _ := s.ReconstructMojette(2, 2, 2, projs)       // []any of grid blocks
canDo   := s.Reconstructible(2, 2, dirs)               // bool
```

### Reflective dispatch

`Call(ctx, method, args...)` routes a snake_case method name to the matching
operation, coercing Ruby-decoded arguments and returning Ruby-shaped values:

```go
full, _ := s.Call(ctx, "encode_rs", 2, 2, dataShards)
rec,  _ := s.Call(ctx, "reconstruct_rs", 2, 2, damaged, present)
ok,   _ := s.Call(ctx, "verify_rs", 2, 2, full)
projs, _ := s.Call(ctx, "encode_mojette", 2, 2, 2, grid, dirs)
grid,  _ := s.Call(ctx, "reconstruct_mojette", 2, 2, 2, projs)
canDo, _ := s.Call(ctx, "reconstructible", 2, 2, dirs)
```

Unknown methods and argument count or type mismatches return a clear error.

See [`examples/erasure_usage.rb`](examples/erasure_usage.rb) for the intended
Ruby usage.

## License

BSD-3-Clause. See [LICENSE](LICENSE).
