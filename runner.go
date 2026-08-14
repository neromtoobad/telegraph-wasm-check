package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Module wraps a loaded scoring module and drives it the way a Telegraph node does:
// strings are written into the module's own linear memory via its exported alloc, then
// rank_answer is called with (ptr, len) pairs.
type Module struct {
	ctx     context.Context
	runtime wazero.Runtime
	mod     api.Module
	mem     api.Memory

	Alloc     api.Function
	Dealloc   api.Function
	Rank      api.Function
	Breakdown api.Function // optional
	Embed     api.Function // optional
	Cached    api.Function // optional
}

// Load instantiates a module with no host imports registered.
//
// That is deliberate and is itself a check: the scoring sandbox gives a module linear
// memory and nothing else — no network, no filesystem, no clock. A binary that imports
// anything from the environment fails here, which is the same outcome it would get on a
// node.
func Load(path string) (*Module, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)

	mod, err := rt.Instantiate(ctx, bytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("instantiate: %w", err)
	}

	m := &Module{
		ctx:       ctx,
		runtime:   rt,
		mod:       mod,
		mem:       mod.Memory(),
		Alloc:     mod.ExportedFunction("alloc"),
		Dealloc:   mod.ExportedFunction("dealloc"),
		Rank:      mod.ExportedFunction("rank_answer"),
		Breakdown: mod.ExportedFunction("breakdown_answer"),
		Embed:     mod.ExportedFunction("embed"),
		Cached:    mod.ExportedFunction("rank_answer_cached"),
	}
	return m, nil
}

func (m *Module) Close() {
	if m.mod != nil {
		m.mod.Close(m.ctx)
	}
	if m.runtime != nil {
		m.runtime.Close(m.ctx)
	}
}

func (m *Module) HasMemory() bool { return m.mem != nil }

// MemorySize returns the module's current linear-memory size in bytes.
func (m *Module) MemorySize() uint32 {
	if m.mem == nil {
		return 0
	}
	return m.mem.Size()
}

// write hands a string to the module. Empty strings are passed as a (0, 0) null pair
// rather than a zero-length allocation, matching how a host avoids a pointless alloc.
func (m *Module) write(s string) (uint32, uint32, error) {
	if len(s) == 0 {
		return 0, 0, nil
	}
	res, err := m.Alloc.Call(m.ctx, uint64(len(s)))
	if err != nil {
		return 0, 0, fmt.Errorf("alloc(%d): %w", len(s), err)
	}
	p := uint32(res[0])
	if !m.mem.Write(p, []byte(s)) {
		return 0, 0, fmt.Errorf("write of %d bytes at ptr %d rejected by module memory", len(s), p)
	}
	return p, uint32(len(s)), nil
}

// Score calls rank_answer. A returned error means the module trapped, which Stage 1
// treats as a hard reject.
func (m *Module) Score(question, groundTruth, answer string) (float32, error) {
	qp, ql, err := m.write(question)
	if err != nil {
		return 0, err
	}
	gp, gl, err := m.write(groundTruth)
	if err != nil {
		return 0, err
	}
	ap, al, err := m.write(answer)
	if err != nil {
		return 0, err
	}

	res, err := m.Rank.Call(m.ctx,
		uint64(qp), uint64(ql), uint64(gp), uint64(gl), uint64(ap), uint64(al))
	if err != nil {
		return 0, fmt.Errorf("rank_answer trapped: %w", err)
	}
	if len(res) == 0 {
		return 0, fmt.Errorf("rank_answer returned no value")
	}
	return api.DecodeF32(res[0]), nil
}

// Breakdown reads the optional breakdown_answer export: a pointer to five consecutive
// f32s, [relevance, correctness, lexical, length_quality, composite].
func (m *Module) BreakdownOf(question, groundTruth, answer string) ([5]float32, error) {
	var out [5]float32
	if m.Breakdown == nil {
		return out, fmt.Errorf("module does not export breakdown_answer")
	}
	qp, ql, _ := m.write(question)
	gp, gl, _ := m.write(groundTruth)
	ap, al, _ := m.write(answer)

	res, err := m.Breakdown.Call(m.ctx,
		uint64(qp), uint64(ql), uint64(gp), uint64(gl), uint64(ap), uint64(al))
	if err != nil {
		return out, err
	}
	ptr := uint32(res[0])
	for i := 0; i < 5; i++ {
		v, ok := m.mem.ReadFloat32Le(ptr + uint32(i*4))
		if !ok {
			return out, fmt.Errorf("could not read float %d at ptr %d", i, ptr)
		}
		out[i] = v
	}
	return out, nil
}
