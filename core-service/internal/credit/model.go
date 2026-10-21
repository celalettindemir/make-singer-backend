package credit

// Costs is the credit price of each billable operation.
type Costs struct {
	RenderMix      int
	RenderFocus    int
	RenderStems    int
	LyricsGenerate int
	LyricsRewrite  int
	Master         int
}

// Operation names a billable operation.
type Operation string

const (
	OpRenderMix      Operation = "render_mix"
	OpRenderFocus    Operation = "render_focus"
	OpRenderStems    Operation = "render_stems"
	OpLyricsGenerate Operation = "lyrics_generate"
	OpLyricsRewrite  Operation = "lyrics_rewrite"
	OpMaster         Operation = "master"
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
	case OpMaster:
		return c.Master
	default:
		return 0
	}
}

// Tier is a plan tier.
type Tier string

// Plan tiers.
const (
	TierPlus Tier = "plus"
	TierPro  Tier = "pro"
)

// LotSource is where a credit lot came from (kredi.dilim.kaynak).
type LotSource string

// Lot sources (DB values).
const (
	LotPlan     LotSource = "plan"
	LotCampaign LotSource = "kampanya"
	LotPack     LotSource = "paket"
	LotAdmin    LotSource = "yonetici"
)

func (s LotSource) valid() bool {
	switch s {
	case LotPlan, LotCampaign, LotPack, LotAdmin:
		return true
	}
	return false
}

// JSONSource is the API name of the source.
func (s LotSource) JSONSource() string {
	switch s {
	case LotPlan:
		return "plan"
	case LotCampaign:
		return "campaign"
	case LotPack:
		return "pack"
	case LotAdmin:
		return "admin"
	}
	return string(s)
}

var movementKinds = map[string]string{
	"render":        "render",
	"render_iade":   "render_refund",
	"soz":           "lyrics",
	"soz_iade":      "lyrics_refund",
	"master":        "master",
	"master_iade":   "master_refund",
	"kampanya":      "campaign_gift",
	"paket":         "pack",
	"plan_dolum":    "plan_refill",
	"plan_bitis":    "plan_end",
	"plan_sure":     "plan_expire",
	"kampanya_sure": "gift_expire",
	"yonetici":      "admin_adjust",
	"yonetici_sure": "admin_expire",
	"magaza_iade":   "store_refund",
}

// MovementKind maps a ledger movement kind (kredi.hareket.tur) to its API
// name. An unknown kind is returned unchanged.
func MovementKind(tur string) string {
	if k, ok := movementKinds[tur]; ok {
		return k
	}
	return tur
}
