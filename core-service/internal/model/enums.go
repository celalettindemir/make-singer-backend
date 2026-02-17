package model

// Genre types
type Genre string

const (
	GenrePop        Genre = "pop"
	GenreRock       Genre = "rock"
	GenreHiphop     Genre = "hiphop"
	GenreRnb        Genre = "rnb"
	GenreElectronic Genre = "electronic"
	GenreJazz       Genre = "jazz"
	GenreCountry    Genre = "country"
	GenreFolk       Genre = "folk"
	GenreClassical  Genre = "classical"
	GenreLatin      Genre = "latin"
	GenreReggae     Genre = "reggae"
	GenreBlues      Genre = "blues"
)

var ValidGenres = []Genre{
	GenrePop, GenreRock, GenreHiphop, GenreRnb, GenreElectronic,
	GenreJazz, GenreCountry, GenreFolk, GenreClassical, GenreLatin,
	GenreReggae, GenreBlues,
}

// Section types
type SectionType string

const (
	SectionIntro        SectionType = "intro"
	SectionVerse        SectionType = "verse"
	SectionPrechorus    SectionType = "prechorus"
	SectionChorus       SectionType = "chorus"
	SectionBridge       SectionType = "bridge"
	SectionOutro        SectionType = "outro"
	SectionInstrumental SectionType = "instrumental"
)

var ValidSectionTypes = []SectionType{
	SectionIntro, SectionVerse, SectionPrechorus, SectionChorus,
	SectionBridge, SectionOutro, SectionInstrumental,
}

// Instruments
type Instrument string

const (
	InstrumentDrums      Instrument = "drums"
	InstrumentBass       Instrument = "bass"
	InstrumentPiano      Instrument = "piano"
	InstrumentGuitar     Instrument = "guitar"
	InstrumentSynth      Instrument = "synth"
	InstrumentStrings    Instrument = "strings"
	InstrumentBrass      Instrument = "brass"
	InstrumentWoodwinds  Instrument = "woodwinds"
	InstrumentPercussion Instrument = "percussion"
	InstrumentPads       Instrument = "pads"
	InstrumentLead       Instrument = "lead"
	InstrumentFX         Instrument = "fx"
)

// Job status
type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCanceled  JobStatus = "canceled"
)

// BPM modes
type BPMMode string

const (
	BPMModeAuto  BPMMode = "auto"
	BPMModeRange BPMMode = "range"
	BPMModeFixed BPMMode = "fixed"
)

// Key modes
type KeyMode string

const (
	KeyModeAuto   KeyMode = "auto"
	KeyModeManual KeyMode = "manual"
)

// Tonic notes
type Tonic string

const (
	TonicC      Tonic = "C"
	TonicCSharp Tonic = "C#"
	TonicD      Tonic = "D"
	TonicDSharp Tonic = "D#"
	TonicE      Tonic = "E"
	TonicF      Tonic = "F"
	TonicFSharp Tonic = "F#"
	TonicG      Tonic = "G"
	TonicGSharp Tonic = "G#"
	TonicA      Tonic = "A"
	TonicASharp Tonic = "A#"
	TonicB      Tonic = "B"
)

// Scales
type Scale string

const (
	ScaleMajor Scale = "major"
	ScaleMinor Scale = "minor"
)

// Density levels
type Density string

const (
	DensityMinimal Density = "minimal"
	DensityMedium  Density = "medium"
	DensityFull    Density = "full"
)

// Groove types
type Groove string

const (
	GrooveStraight Groove = "straight"
	GrooveSwing    Groove = "swing"
	GrooveHalfTime Groove = "half_time"
)

// Master profiles
type MasterProfile string

const (
	MasterProfileClean MasterProfile = "clean"
	MasterProfileWarm  MasterProfile = "warm"
	MasterProfileLoud  MasterProfile = "loud"
)

// Mix presets
type MixPreset string

const (
	MixPresetDefault      MixPreset = "default"
	MixPresetVocalFriendly MixPreset = "vocal_friendly"
	MixPresetBassHeavy    MixPreset = "bass_heavy"
	MixPresetBright       MixPreset = "bright"
	MixPresetWarm         MixPreset = "warm"
)

// Language
type Language string

const (
	LanguageEN Language = "en"
	LanguageTR Language = "tr"
	LanguageFR Language = "fr"
)
