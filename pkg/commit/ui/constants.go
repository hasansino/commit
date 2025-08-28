package ui

// ---- Checkboxes ----

const (
	CheckboxIDDryRun         = "dry_run"
	CheckboxIDPush           = "push"
	CheckboxIDCreateTagMajor = "create_tag_major"
	CheckboxIDCreateTagMinor = "create_tag_minor"
	CheckboxIDCreateTagPatch = "create_tag_patch"
)

const (
	CheckboxLabelDryRun         = "Dry run"
	CheckboxLabelPush           = "Push to remote"
	CheckboxLabelCreateTagMajor = "Tag (major)"
	CheckboxLabelCreateTagMinor = "Tag (minor)"
	CheckboxLabelCreateTagPatch = "Tag (patch)"
)

const (
	CheckboxKeymap1 = "1"
	CheckboxKeymap2 = "2"
	CheckboxKeymap3 = "3"
	CheckboxKeymap4 = "4"
	CheckboxKeymap5 = "5"
)

var checkboxKeymaps = map[string]string{
	CheckboxIDDryRun:         CheckboxKeymap1,
	CheckboxIDPush:           CheckboxKeymap2,
	CheckboxIDCreateTagMajor: CheckboxKeymap3,
	CheckboxIDCreateTagMinor: CheckboxKeymap4,
	CheckboxIDCreateTagPatch: CheckboxKeymap5,
}

var checkboxDefaults = map[string]bool{
	CheckboxIDDryRun:         false,
	CheckboxIDPush:           false,
	CheckboxIDCreateTagMajor: false,
	CheckboxIDCreateTagMinor: false,
	CheckboxIDCreateTagPatch: false,
}

type Checkbox struct {
	id    string
	key   string
	label string
}

var footerCheckboxes = []Checkbox{
	{CheckboxIDDryRun, CheckboxKeymap1, CheckboxLabelDryRun},
	{CheckboxIDPush, CheckboxKeymap2, CheckboxLabelPush},
	{CheckboxIDCreateTagMajor, CheckboxKeymap3, CheckboxLabelCreateTagMajor},
	{CheckboxIDCreateTagMinor, CheckboxKeymap4, CheckboxLabelCreateTagMinor},
	{CheckboxIDCreateTagPatch, CheckboxKeymap5, CheckboxLabelCreateTagPatch},
}

func IsTagCheckbox(id string) bool {
	return id == CheckboxIDCreateTagMajor ||
		id == CheckboxIDCreateTagMinor ||
		id == CheckboxIDCreateTagPatch
}

// ----

// UI Text
const (
	ListTitle         = "Select Commit Message"
	ManualOptionTitle = "Write custom message"
	ManualOptionDesc  = "Enter your own commit message"
	ManualInputTitle  = "Write Your Commit Message"
	ManualInputHelp   = "Enter: new line • Ctrl+D: finish • Esc: cancel"
	FooterHelp        = "Press 1-5 to toggle options"
	ProviderManual    = "manual"
)

// Unicode Characters
const (
	CheckboxChecked   = "▣"
	CheckboxUnchecked = "▢"
	Cursor            = "│"
)

// ANSI 256 Colors (8-bit)
const (
	ColorPrimary      = "170"
	ColorSecondary    = "255"
	ColorNormal       = "250"
	ColorDimmed       = "240"
	ColorDimmedDark   = "238"
	ColorDimmedDarker = "236"
	ColorBorder       = "240"
	ColorAccent       = "62"
	ColorBright       = "230"
	ColorMuted        = "241"
	ColorWarning      = "214"
)

// Layout Constants
const (
	PaddingTop        = 2
	PaddingHorizontal = 4
	// Approximate height needed for footer (border + checkbox line + help text)
	FooterHeightApprox = 5
	DefaultListHeight  = 10
	MaxListHeight      = 15
	MinListHeight      = 3
	MaxDisplayLines    = 10 // Max lines to show in multi-line preview
	MaxDescriptionLen  = 60 // Max length for single-line description
	ManualInputWidth   = 80
	ManualInputHeight  = 1
)

// Keybindings
const (
	KeyQuit        = "q"
	KeySelect      = "enter"
	KeyCancel      = "esc"
	KeyNewLine     = "enter"
	KeyFinishInput = "ctrl+d"
	KeyBackspace   = "backspace"
	KeySpace       = " "
	KeyInterrupt   = "ctrl+c"
)

const minCommitMessageLength = 3
