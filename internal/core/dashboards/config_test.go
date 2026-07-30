package dashboards

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The configuration vocabulary: closed, per gadget kind, and refused rather
// than dropped. Every case is written so that LOOSENING the parser fails it.

func def(t *testing.T, k GadgetKey) Definition {
	t.Helper()
	d, ok := Lookup(k)
	require.True(t, ok)
	return d
}

func TestParseConfig_AcceptsWhatTheKindDeclares(t *testing.T) {
	c, err := ParseConfig(def(t, GadgetViewResults), []byte(`{"title":"Open bugs","limit":10}`))
	require.NoError(t, err)
	require.Equal(t, "Open bugs", c.Title)
	require.NotNil(t, c.Limit)
	require.Equal(t, 10, *c.Limit)
	require.Equal(t, 10, c.RowLimit())
}

func TestParseConfig_EmptyIsTheZeroConfig(t *testing.T) {
	c, err := ParseConfig(def(t, GadgetNote), nil)
	require.NoError(t, err)
	require.Equal(t, Config{}, c)
	require.Equal(t, DefaultGadgetLimit, c.RowLimit(), "an unset limit is the default, never zero rows")
}

// A key in the vocabulary but not in THIS kind's set is a mistake worth
// naming. Dropping it silently would leave the author looking at a note that
// ignores the field they set.
//
// Fails-before: remove the AllowsConfigKey loop from ParseConfig and this
// passes with the group_by stored and never read.
func TestParseConfig_RefusesAKeyThisKindDoesNotCarry(t *testing.T) {
	_, err := ParseConfig(def(t, GadgetNote), []byte(`{"body":"hi","group_by":"status"}`))
	require.ErrorIs(t, err, ErrUnknownConfigKey)
	require.Contains(t, err.Error(), "group_by")
	require.Contains(t, err.Error(), string(GadgetNote))

	_, err = ParseConfig(def(t, GadgetViewCount), []byte(`{"limit":5}`))
	require.ErrorIs(t, err, ErrUnknownConfigKey, "a stat gadget shows one number; a row limit is meaningless on it")
}

// A key in no kind's vocabulary is refused by the decoder itself, so a
// document this build does not understand can never reach the table.
func TestParseConfig_RefusesAKeyTheVocabularyDoesNotDefine(t *testing.T) {
	_, err := ParseConfig(def(t, GadgetNote), []byte(`{"colour":"red"}`))
	require.ErrorIs(t, err, ErrUnknownConfigKey)
}

// A second document after the first is refused rather than silently taking the
// first — the first pass is a plain json.Unmarshal, which rejects it, so
// ParseConfig deliberately carries no separate trailing-content check.
func TestParseConfig_RefusesTrailingContent(t *testing.T) {
	_, err := ParseConfig(def(t, GadgetNote), []byte(`{"body":"a"}{"body":"b"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "malformed gadget configuration")
}

// A malformed document is refused, and a JSON null is the empty configuration
// rather than an error — a gadget with nothing set is an ordinary state.
func TestParseConfig_MalformedAndNull(t *testing.T) {
	_, err := ParseConfig(def(t, GadgetNote), []byte(`["body"]`))
	require.Error(t, err, "a config document is an object, never an array")

	c, err := ParseConfig(def(t, GadgetNote), []byte(`null`))
	require.NoError(t, err)
	require.Equal(t, Config{}, c)
}

func TestParseConfig_BoundsTheRowLimit(t *testing.T) {
	for _, n := range []int{0, -1, MaxGadgetLimit + 1} {
		_, err := ParseConfig(def(t, GadgetViewResults), []byte(`{"limit":`+itoa(n)+`}`))
		require.Error(t, err, "limit %d must be refused", n)
	}
	_, err := ParseConfig(def(t, GadgetViewResults), []byte(`{"limit":`+itoa(MaxGadgetLimit)+`}`))
	require.NoError(t, err, "the bound itself is legal")
}

func TestParseConfig_BoundsTheTitleAndTheNoteBody(t *testing.T) {
	long := strings.Repeat("x", MaxTitleLen+1)
	_, err := ParseConfig(def(t, GadgetNote), []byte(`{"title":"`+long+`"}`))
	require.Error(t, err)

	body, _ := json.Marshal(strings.Repeat("y", MaxNoteLen+1))
	_, err = ParseConfig(def(t, GadgetNote), []byte(`{"body":`+string(body)+`}`))
	require.Error(t, err)
}

// Rune counting, not byte counting: a note in a non-Latin script must not be
// refused at a third of its stated length.
func TestParseConfig_CountsRunesNotBytes(t *testing.T) {
	body, err := json.Marshal(strings.Repeat("あ", MaxNoteLen))
	require.NoError(t, err)
	_, err = ParseConfig(def(t, GadgetNote), []byte(`{"body":`+string(body)+`}`))
	require.NoError(t, err, "MaxNoteLen is a rune bound; multibyte text must not be cut short")
}

func TestParseConfig_RefusesAnUnknownBreakdownField(t *testing.T) {
	_, err := ParseConfig(def(t, GadgetBreakdown), []byte(`{"group_by":"space"}`))
	require.Error(t, err)
}

// A breakdown with no field can only ever render "nothing to show". Refusing
// it at write time is the difference between a validation message and a tile
// nobody can explain.
func TestParseConfig_BreakdownNeedsAField(t *testing.T) {
	_, err := ParseConfig(def(t, GadgetBreakdown), []byte(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "group by")

	_, err = ParseConfig(def(t, GadgetBreakdown), []byte(`{"group_by":"status"}`))
	require.NoError(t, err)
}

func TestConfigEncode_RoundTrips(t *testing.T) {
	d := def(t, GadgetViewResults)
	n := 7
	raw, err := Config{Title: "Mine", Limit: &n}.Encode(d)
	require.NoError(t, err)

	back, err := ParseConfig(d, raw)
	require.NoError(t, err)
	require.Equal(t, "Mine", back.Title)
	require.Equal(t, 7, *back.Limit)
}

// Encode validates before it marshals, so a document in the table is by
// construction one this build produced and understands — the same rule
// views.Query.Encode enforces on the filter document.
func TestConfigEncode_RefusesAnInvalidConfig(t *testing.T) {
	bad := 999
	_, err := Config{Limit: &bad}.Encode(def(t, GadgetViewResults))
	require.Error(t, err)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
