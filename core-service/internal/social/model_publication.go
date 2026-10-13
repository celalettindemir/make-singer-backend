package social

import "time"

// MusicalKey is contract §4.1: { tonik, mod }.
type MusicalKey struct {
	Tonic string `json:"tonik"`
	Mode  string `json:"mod"`
}

// StructureItem is one section of the song structure.
type StructureItem struct {
	ID   string  `json:"id"`
	Type string  `json:"tur"`
	Bars float64 `json:"olcu"`
}

// LyricsSection and Lyrics are contract §4.1 Sozler.
type LyricsSection struct {
	SectionID string   `json:"bolumId"`
	Type      string   `json:"tur"`
	Lines     []string `json:"satirlar"`
}

type Lyrics struct {
	Sections []LyricsSection `json:"bolumler"`
}

// SourceSummary is a cover's original. If the original was deleted, Deleted
// is true and the other fields hold the last known values.
type SourceSummary struct {
	PublicationID string `json:"yayinId"`
	Title         string `json:"baslik"`
	OwnerUsername string `json:"sahipKullaniciAdi"`
	Deleted       bool   `json:"silindi"`
}

// PublicationSummary is contract §4.1. Source is VISIBLE as null on a
// publication that is not a cover (no omitempty); Waveform is never null.
type PublicationSummary struct {
	ID             string         `json:"id"`
	Slug           string         `json:"slug"`
	Title          string         `json:"baslik"`
	Owner          ProfileSummary `json:"sahip"`
	Genre          string         `json:"tur"`
	BPM            int            `json:"bpm"`
	MusicalKey     MusicalKey     `json:"ton"`
	DurationSec    int            `json:"sureSn"`
	Waveform       []float64      `json:"dalgaFormu"`
	LikeCount      int            `json:"begeniSayisi"`
	CommentCount   int            `json:"yorumSayisi"`
	CoverCount     int            `json:"coverSayisi"`
	LikedByMe      bool           `json:"begendimMi"`
	BackingEnabled bool           `json:"altyapiAcik"`
	Source         *SourceSummary `json:"kaynak"`
	CreatedAt      time.Time      `json:"olusturma"`
}

// PublicationDetail is contract §4.1. Visibility is always sent.
type PublicationDetail struct {
	PublicationSummary
	Description     string          `json:"aciklama"`
	CommentsEnabled bool            `json:"yorumAcik"`
	Lyrics          Lyrics          `json:"sozler"`
	Structure       []StructureItem `json:"yapi"`
	Status          string          `json:"durum"`
	IsMine          bool            `json:"benimMi"`
	Visibility      string          `json:"gorunurluk"`
}

// StemInput is one stem in a publication request. URL is the render
// result's fileUrl (a full address or a bare key).
type StemInput struct {
	ID         string  `json:"id"`
	Instrument string  `json:"enstruman"`
	URL        string  `json:"url"`
	Duration   float64 `json:"sure"`
}

// PublicationRequest is the POST /yayinlar body (mobile PublishInput).
type PublicationRequest struct {
	Title               string          `json:"baslik"`
	Description         string          `json:"aciklama"`
	Visibility          string          `json:"gorunurluk"`
	BackingEnabled      bool            `json:"altyapiAcik"`
	CommentsEnabled     bool            `json:"yorumAcik"`
	MasterURL           string          `json:"masterUrl"`
	Stems               []StemInput     `json:"stemler"`
	Lyrics              Lyrics          `json:"sozler"`
	Structure           []StructureItem `json:"yapi"`
	Genre               string          `json:"tur"`
	BPM                 int             `json:"bpm"`
	MusicalKey          MusicalKey      `json:"ton"`
	DurationSec         int             `json:"sureSn"`
	SourcePublicationID *string         `json:"kaynakYayinId"`
}

// PublicationResponse is the POST /yayinlar 202 body.
type PublicationResponse struct {
	ID     string `json:"id"`
	Slug   string `json:"slug"`
	Status string `json:"durum"`
}

// PublicationPatch is PATCH /yayinlar/{id}; only the fields that are sent change.
type PublicationPatch struct {
	Description     *string `json:"aciklama"`
	BackingEnabled  *bool   `json:"altyapiAcik"`
	Visibility      *string `json:"gorunurluk"`
	CommentsEnabled *bool   `json:"yorumAcik"`
}

// FileLink and PublicationFiles are the POST /yayinlar/{id}/dosyalar response.
type FileLink struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type PublicationFiles struct {
	Master FileLink `json:"master"`
}

// PreparationStem and Preparation are the yayin.hazirlik column: the source
// keys to copy. They stay on the row so that retry can run again with the
// same input; they are never returned to clients.
type PreparationStem struct {
	ID         string  `json:"id"`
	Instrument string  `json:"enstruman"`
	Duration   float64 `json:"sure"`
	Key        string  `json:"key"`
}

type Preparation struct {
	MasterKey string            `json:"masterKey"`
	Stems     []PreparationStem `json:"stems"`
}

// StemRecord is an item of yayin.stem_listesi: a copied stem. Key is the
// sosyal/{pubId}/stems/{stemId}.{ext} copy.
type StemRecord struct {
	ID         string  `json:"id"`
	Instrument string  `json:"enstruman"`
	Duration   float64 `json:"sure"`
	Key        string  `json:"anahtar"`
}

// PublicationInsert is a validated publication to be written to the store.
type PublicationInsert struct {
	ID                  string
	Slug                string
	OwnerID             string
	Title               string
	Description         string
	Genre               string
	BPM                 int
	MusicalKey          MusicalKey
	DurationSec         int
	Visibility          string
	BackingEnabled      bool
	CommentsEnabled     bool
	Lyrics              Lyrics
	Structure           []StructureItem
	Preparation         Preparation
	SourcePublicationID string // "" means original
	SourceTitle         string
	SourceOwnerUsername string
}

// PublicationRecord is the detail read from the store plus the internal
// fields the service needs for decisions. Deleted publications are returned
// too (Deleted true); the service applies visibility with CanSeeDetail.
type PublicationRecord struct {
	PublicationDetail
	OwnerID             string
	SourcePublicationID string
	HasStems            bool // the preparation has stems: backing can be enabled later
	MasterKey           string
	Deleted             bool
	ViewerFollows       bool // the viewer follows the owner
}

// visibilityInput turns the record into a visibility input for the viewer.
func (r *PublicationRecord) visibilityInput(viewerID string) VisibilityInput {
	return VisibilityInput{
		OwnerID: r.OwnerID, ViewerID: viewerID, Visibility: r.Visibility,
		Status: r.Status, Deleted: r.Deleted, ViewerFollows: r.ViewerFollows,
	}
}

// PreparationRecord is the row that the sosyal:hazirla task reads.
type PreparationRecord struct {
	ID          string
	OwnerID     string
	Status      string
	Deleted     bool
	Preparation Preparation
}

// PreparationResult is what the task writes to the row when it finishes successfully.
type PreparationResult struct {
	MasterKey  string
	Stems      []StemRecord
	Waveform   []float64
	PreviewKey string // "" means no preview
}
