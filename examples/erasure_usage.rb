# frozen_string_literal: true
#
# Pure-Ruby usage of the `erasure` gem (github.com/go-ruby-erasure/erasure),
# as bound by the go-embedded-ruby interpreter (rbgo). The Go core is a thin
# reflective adapter over github.com/go-erasure/reedsolomon (Reed-Solomon over
# GF(2^16)) and github.com/go-erasure/mojette (the Mojette / discrete-Radon
# erasure code). Every call takes and returns plain Ruby Arrays, Hashes,
# Strings (byte buffers) and Integers.
#
# This file is documentation of the binding surface; it is comment-only and is
# not executed by CI.

require "erasure"

session = Erasure::Session.new

## Reed-Solomon --------------------------------------------------------------
#
# Shards are byte strings, all of the same even length. Encode two data shards
# into a set with two parity shards; the result is the full data+parity set.
data = ["\x00\x01\x02\x03", "\x10\x11\x12\x13"]
shards = session.encode_rs(2, 2, data)  # => Array of 4 byte strings

# Verify that parity is consistent with data.
session.verify_rs(2, 2, shards)         # => true

# Simulate the loss of one data shard and one parity shard, then recover. The
# "present" mask is an Array of booleans, one per shard, in shard order.
present = [false, true, true, false]
damaged = [nil, shards[1], shards[2], nil]
recovered = session.reconstruct_rs(2, 2, damaged, present)
raise "lost data" unless recovered == shards

## Mojette -------------------------------------------------------------------
#
# A grid is rows*cols blocks of `block_size` bytes in row-major order. Each
# direction is a [p, q] pair. Encoding yields one projection Hash per
# direction: {"p" => Integer, "q" => Integer, "bins" => Array of byte strings}.
grid       = ["\x01\x02", "\x03\x04", "\x05\x06", "\x07\x08"] # 2x2 grid, 2 bytes/block
dirs       = [[0, 1], [1, 1], [-1, 1], [2, 1]]
projections = session.encode_mojette(2, 2, 2, grid, dirs)

# Given a Katz-sufficient subset of projections (here, any three of the four),
# the grid is rebuilt block-for-block.
if session.reconstructible(2, 2, dirs.first(3))
  rebuilt = session.reconstruct_mojette(2, 2, 2, projections.first(3))
  raise "lost grid" unless rebuilt == grid
end

## Reflective dispatch -------------------------------------------------------
#
# All of the above route through a single Call entry point on the Go side; the
# rbgo binding drives it from method_missing, so the snake_case method names
# above are the whole API. Unknown methods and argument mismatches raise.
