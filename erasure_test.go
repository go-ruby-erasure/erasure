package erasure

import (
	"bytes"
	"context"
	"testing"
)

func TestNewSessionOptions(t *testing.T) {
	called := false
	s := NewSession(func(*Session) { called = true })
	if s == nil || !called {
		t.Fatal("option not applied")
	}
}

// --- Reed-Solomon -----------------------------------------------------------

func rsData() [][]byte {
	return [][]byte{
		{0x00, 0x01, 0x02, 0x03},
		{0x10, 0x11, 0x12, 0x13},
	}
}

func TestEncodeVerifyReconstructRS(t *testing.T) {
	s := NewSession()
	full, err := s.EncodeRS(2, 2, rsData())
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 4 {
		t.Fatalf("full set = %d shards", len(full))
	}
	shards := arrayToBytes(t, full)

	ok, err := s.VerifyRS(2, 2, shards)
	if err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}

	// Erase one data shard and one parity shard, then reconstruct.
	present := []bool{false, true, true, false}
	damaged := [][]byte{nil, shards[1], shards[2], nil}
	rec, err := s.ReconstructRS(2, 2, damaged, present)
	if err != nil {
		t.Fatal(err)
	}
	got := arrayToBytes(t, rec)
	for i := range shards {
		if !bytes.Equal(got[i], shards[i]) {
			t.Errorf("shard %d = %v, want %v", i, got[i], shards[i])
		}
	}
}

func TestEncodeRSErrors(t *testing.T) {
	s := NewSession()
	if _, err := s.EncodeRS(0, 1, rsData()); err == nil {
		t.Error("bad shard counts should error")
	}
	if _, err := s.EncodeRS(2, 2, rsData()[:1]); err == nil {
		t.Error("wrong data-shard count should error")
	}
	// Odd-length shards are rejected by the encoder.
	if _, err := s.EncodeRS(2, 1, [][]byte{{1, 2, 3}, {4, 5, 6}}); err == nil {
		t.Error("odd-length shards should error")
	}
}

func TestReconstructRSErrors(t *testing.T) {
	s := NewSession()
	if _, err := s.ReconstructRS(0, 1, nil, nil); err == nil {
		t.Error("bad shard counts should error")
	}
	if _, err := s.ReconstructRS(2, 2, [][]byte{nil, nil, nil}, []bool{true, true, true}); err == nil {
		t.Error("wrong shard count should error")
	}
	if _, err := s.ReconstructRS(2, 2, make([][]byte, 4), []bool{true, true, true}); err == nil {
		t.Error("wrong present count should error")
	}
	// Too many erasures to recover -> encoder error (shardLen stays 0).
	if _, err := s.ReconstructRS(2, 2, make([][]byte, 4), []bool{false, false, false, false}); err == nil {
		t.Error("unrecoverable erasure should error")
	}
}

func TestVerifyRSError(t *testing.T) {
	s := NewSession()
	if _, err := s.VerifyRS(0, 1, nil); err == nil {
		t.Error("bad shard counts should error")
	}
}

// --- Mojette ----------------------------------------------------------------

func mojDirs() [][2]int { return [][2]int{{0, 1}, {1, 1}, {-1, 1}, {2, 1}} }
func mojGrid() [][]byte { return [][]byte{{1, 2}, {3, 4}, {5, 6}, {7, 8}} }

func TestEncodeReconstructMojette(t *testing.T) {
	s := NewSession()
	projs, err := s.EncodeMojette(2, 2, 2, mojGrid(), mojDirs())
	if err != nil {
		t.Fatal(err)
	}
	if len(projs) != 4 {
		t.Fatalf("projections = %d", len(projs))
	}
	first := projs[0].(map[string]any)
	if first["p"] != 0 || first["q"] != 1 {
		t.Errorf("first projection dir = %v", first)
	}
	if _, ok := first["bins"].([]any); !ok {
		t.Errorf("bins shape = %T", first["bins"])
	}

	// Drop one projection (a Katz-sufficient subset remains) and reconstruct.
	rec, err := s.ReconstructMojette(2, 2, 2, projs[:3])
	if err != nil {
		t.Fatal(err)
	}
	got := arrayToBytes(t, rec)
	for i, want := range mojGrid() {
		if !bytes.Equal(got[i], want) {
			t.Errorf("block %d = %v, want %v", i, got[i], want)
		}
	}
}

func TestEncodeMojetteError(t *testing.T) {
	s := NewSession()
	// Grid data length mismatch.
	if _, err := s.EncodeMojette(2, 2, 2, [][]byte{{1, 2}}, mojDirs()); err == nil {
		t.Error("bad grid should error")
	}
}

func TestReconstructMojetteErrors(t *testing.T) {
	s := NewSession()
	projs, err := s.EncodeMojette(2, 2, 2, mojGrid(), mojDirs())
	if err != nil {
		t.Fatal(err)
	}
	// Insufficient projections.
	if _, err := s.ReconstructMojette(2, 2, 2, projs[:1]); err == nil {
		t.Error("insufficient projections should error")
	}
	// Malformed projection Hash surfaces a parse error.
	if _, err := s.ReconstructMojette(2, 2, 2, []any{"not a hash"}); err == nil {
		t.Error("bad projection should error")
	}
}

func TestReconstructible(t *testing.T) {
	s := NewSession()
	if !s.Reconstructible(2, 2, mojDirs()) {
		t.Error("full direction set should be reconstructible")
	}
	if s.Reconstructible(2, 2, mojDirs()[:1]) {
		t.Error("single direction should not be reconstructible")
	}
}

// --- Call dispatch ----------------------------------------------------------

func TestCallReedSolomon(t *testing.T) {
	s := NewSession()
	ctx := context.Background()

	// encode_rs with rbgo-style args (int64 counts, []any of []byte shards).
	shardsAny := []any{rsData()[0], rsData()[1]}
	res, err := s.Call(ctx, "encode_rs", int64(2), int64(2), shardsAny)
	if err != nil {
		t.Fatal(err)
	}
	full := res.([]any)
	if len(full) != 4 {
		t.Fatalf("encode_rs = %d shards", len(full))
	}

	// verify_rs on the full set.
	ok, err := s.Call(ctx, "verify_rs", 2, 2, full)
	if err != nil || ok.(bool) != true {
		t.Fatalf("verify_rs = %v, %v", ok, err)
	}

	// reconstruct_rs after erasing shards.
	damaged := []any{[]byte(nil), full[1], full[2], []byte(nil)}
	present := []any{false, true, true, false}
	rec, err := s.Call(ctx, "reconstruct_rs", 2, 2, damaged, present)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.([]any)) != 4 {
		t.Errorf("reconstruct_rs = %v", rec)
	}
}

func TestCallMojette(t *testing.T) {
	s := NewSession()
	ctx := context.Background()

	dataAny := []any{[]byte{1, 2}, []byte{3, 4}, []byte{5, 6}, []byte{7, 8}}
	dirsAny := []any{[]any{0, 1}, []any{1, 1}, []any{-1, 1}, []any{2, 1}}
	res, err := s.Call(ctx, "encode_mojette", 2, 2, 2, dataAny, dirsAny)
	if err != nil {
		t.Fatal(err)
	}
	projs := res.([]any)

	rec, err := s.Call(ctx, "reconstruct_mojette", 2, 2, 2, projs[:3])
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.([]any)) != 4 {
		t.Errorf("reconstruct_mojette = %v", rec)
	}

	ok, err := s.Call(ctx, "reconstructible", 2, 2, dirsAny)
	if err != nil || ok.(bool) != true {
		t.Fatalf("reconstructible = %v, %v", ok, err)
	}
}

func TestCallUnknownAndArgCount(t *testing.T) {
	s := NewSession()
	ctx := context.Background()
	cases := []struct {
		method string
		args   []any
	}{
		{"nope", nil},
		{"encode_rs", []any{1, 2}},
		{"reconstruct_rs", []any{1, 2, 3}},
		{"verify_rs", []any{1, 2}},
		{"encode_mojette", []any{1, 2, 3, 4}},
		{"reconstruct_mojette", []any{1, 2, 3}},
		{"reconstructible", []any{1, 2}},
	}
	for _, c := range cases {
		if _, err := s.Call(ctx, c.method, c.args...); err == nil {
			t.Errorf("%s%v should error", c.method, c.args)
		}
	}
}

func TestCallArgTypeErrors(t *testing.T) {
	s := NewSession()
	ctx := context.Background()
	goodShards := []any{[]byte{0, 1}, []byte{2, 3}}
	goodDirs := []any{[]any{0, 1}}
	goodProjs := []any{map[string]any{"p": 0, "q": 1, "bins": []any{[]byte{1, 2}}}}

	cases := []struct {
		name   string
		method string
		args   []any
	}{
		// argInt failures at each position.
		{"encode_rs d", "encode_rs", []any{"x", 2, goodShards}},
		{"encode_rs p", "encode_rs", []any{2, "x", goodShards}},
		{"encode_rs shards", "encode_rs", []any{2, 2, "notarray"}},
		{"encode_rs shard-elem", "encode_rs", []any{2, 2, []any{123}}},
		{"reconstruct_rs d", "reconstruct_rs", []any{"x", 2, goodShards, []any{true}}},
		{"reconstruct_rs p", "reconstruct_rs", []any{2, "x", goodShards, []any{true}}},
		{"reconstruct_rs shards", "reconstruct_rs", []any{2, 2, "x", []any{true}}},
		{"reconstruct_rs present", "reconstruct_rs", []any{2, 2, goodShards, "x"}},
		{"reconstruct_rs present-elem", "reconstruct_rs", []any{2, 2, goodShards, []any{"notbool"}}},
		{"verify_rs d", "verify_rs", []any{"x", 2, goodShards}},
		{"verify_rs p", "verify_rs", []any{2, "x", goodShards}},
		{"verify_rs shards", "verify_rs", []any{2, 2, "x"}},
		{"encode_mojette r", "encode_mojette", []any{"x", 2, 2, goodShards, goodDirs}},
		{"encode_mojette c", "encode_mojette", []any{2, "x", 2, goodShards, goodDirs}},
		{"encode_mojette bs", "encode_mojette", []any{2, 2, "x", goodShards, goodDirs}},
		{"encode_mojette data", "encode_mojette", []any{2, 2, 2, "x", goodDirs}},
		{"encode_mojette dirs", "encode_mojette", []any{2, 2, 2, goodShards, "x"}},
		{"encode_mojette dir-shape", "encode_mojette", []any{2, 2, 2, goodShards, []any{[]any{1}}}},
		{"encode_mojette dir-p", "encode_mojette", []any{2, 2, 2, goodShards, []any{[]any{"x", 1}}}},
		{"encode_mojette dir-q", "encode_mojette", []any{2, 2, 2, goodShards, []any{[]any{1, "x"}}}},
		{"reconstruct_mojette r", "reconstruct_mojette", []any{"x", 2, 2, goodProjs}},
		{"reconstruct_mojette c", "reconstruct_mojette", []any{2, "x", 2, goodProjs}},
		{"reconstruct_mojette bs", "reconstruct_mojette", []any{2, 2, "x", goodProjs}},
		{"reconstruct_mojette projs", "reconstruct_mojette", []any{2, 2, 2, "x"}},
		{"reconstructible r", "reconstructible", []any{"x", 2, goodDirs}},
		{"reconstructible c", "reconstructible", []any{2, "x", goodDirs}},
		{"reconstructible dirs", "reconstructible", []any{2, 2, "x"}},
	}
	for _, c := range cases {
		if _, err := s.Call(ctx, c.method, c.args...); err == nil {
			t.Errorf("%s should error", c.name)
		}
	}
}

func TestReconstructMojetteProjectionParsing(t *testing.T) {
	s := NewSession()
	ctx := context.Background()
	bad := []struct {
		name string
		proj any
	}{
		{"not-hash", "x"},
		{"bad-p", map[string]any{"p": "x", "q": 1, "bins": []any{[]byte{1}}}},
		{"bad-q", map[string]any{"p": 0, "q": "x", "bins": []any{[]byte{1}}}},
		{"bad-bins-type", map[string]any{"p": 0, "q": 1, "bins": "x"}},
		{"bad-bins-elem", map[string]any{"p": 0, "q": 1, "bins": []any{123}}},
	}
	for _, c := range bad {
		if _, err := s.Call(ctx, "reconstruct_mojette", 2, 2, 2, []any{c.proj}); err == nil {
			t.Errorf("%s should error", c.name)
		}
	}
}

func TestCoercionHelpers(t *testing.T) {
	if n, err := asInt(int64(5)); n != 5 || err != nil {
		t.Error("asInt int64")
	}
	if n, err := asInt(3.0); n != 3 || err != nil {
		t.Error("asInt float64")
	}
	if _, err := asInt("x"); err == nil {
		t.Error("asInt should reject string")
	}
	if b, err := asBytes("hi"); string(b) != "hi" || err != nil {
		t.Error("asBytes string")
	}
	if _, err := asBytes(42); err == nil {
		t.Error("asBytes should reject int")
	}
}

// arrayToBytes turns a Ruby Array of byte strings back into a [][]byte.
func arrayToBytes(t *testing.T, a []any) [][]byte {
	t.Helper()
	out := make([][]byte, len(a))
	for i, v := range a {
		b, ok := v.([]byte)
		if !ok {
			t.Fatalf("element %d is %T, want []byte", i, v)
		}
		out[i] = b
	}
	return out
}
