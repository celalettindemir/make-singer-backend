package credit

// Costs is the credit price of each billable operation.
type Costs struct {
	RenderMix      int
	RenderFocus    int
	RenderStems    int
	LyricsGenerate int
	LyricsRewrite  int
}

// Operation names a billable operation.
type Operation string

const (
	OpRenderMix      Operation = "render_mix"
	OpRenderFocus    Operation = "render_focus"
	OpRenderStems    Operation = "render_stems"
	OpLyricsGenerate Operation = "lyrics_generate"
	OpLyricsRewrite  Operation = "lyrics_rewrite"
)

// Of returns the cost of op, or 0 for an unknown operation.
func (c Costs) Of(op Operation) int {
	switch op {
	case OpRenderMix:
		return c.RenderMix
	case OpRenderFocus:
		return c.RenderFocus
	case OpRenderStems:
		return c.RenderStems
	case OpLyricsGenerate:
		return c.LyricsGenerate
	case OpLyricsRewrite:
		return c.LyricsRewrite
	default:
		return 0
	}
}
