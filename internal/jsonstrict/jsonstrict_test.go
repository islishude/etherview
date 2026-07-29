package jsonstrict

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAcceptsOneBoundedValue(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`null`,
		`{"value":9007199254740991,"nested":[true,"ok"]}`,
		`[-9007199254740991,0,1]`,
	} {
		if err := Validate([]byte(input), Limits{SafeIntegersOnly: true}); err != nil {
			t.Fatalf("Validate(%s): %v", input, err)
		}
	}
}

func TestValidateRejectsAmbiguousOrUnboundedJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		limits Limits
		want   error
	}{
		{
			name:  "duplicate root key",
			input: `{"value":1,"value":2}`,
			want:  ErrDuplicateKey,
		},
		{
			name:  "duplicate nested key",
			input: `{"outer":{"value":1,"value":2}}`,
			want:  ErrDuplicateKey,
		},
		{
			name:  "trailing value",
			input: `{} []`,
			want:  ErrTrailingData,
		},
		{
			name:  "invalid trailing token",
			input: `{} nope`,
			want:  ErrSyntax,
		},
		{
			name:   "too deep",
			input:  `[[[]]]`,
			limits: Limits{MaxDepth: 2},
			want:   ErrTooDeep,
		},
		{
			name:   "too many nodes",
			input:  `{"one":1,"two":2}`,
			limits: Limits{MaxNodes: 4},
			want:   ErrTooManyNodes,
		},
		{
			name:   "positive unsafe integer",
			input:  `9007199254740992`,
			limits: Limits{SafeIntegersOnly: true},
			want:   ErrUnsafeNumber,
		},
		{
			name:   "negative unsafe integer",
			input:  `-9007199254740992`,
			limits: Limits{SafeIntegersOnly: true},
			want:   ErrUnsafeNumber,
		},
		{
			name:   "fraction",
			input:  `1.0`,
			limits: Limits{SafeIntegersOnly: true},
			want:   ErrUnsafeNumber,
		},
		{
			name:   "exponent",
			input:  `1e2`,
			limits: Limits{SafeIntegersOnly: true},
			want:   ErrUnsafeNumber,
		},
		{
			name:  "empty",
			input: ``,
			want:  ErrSyntax,
		},
		{
			name:  "unterminated",
			input: `{"value":`,
			want:  ErrSyntax,
		},
		{
			name:  "unpaired high surrogate",
			input: `{"value":"\ud800"}`,
			want:  ErrSyntax,
		},
		{
			name:  "unpaired low surrogate",
			input: `{"value":"\udc00"}`,
			want:  ErrSyntax,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := Validate([]byte(test.input), test.limits)
			if !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateRejectsInvalidUTF8RatherThanNormalizingIt(t *testing.T) {
	t.Parallel()
	input := []byte{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'}
	if err := Validate(input, Limits{}); !errors.Is(err, ErrSyntax) {
		t.Fatalf("Validate(invalid UTF-8) error = %v, want %v", err, ErrSyntax)
	}
}

func TestValidateAcceptsSurrogatePairsAndEscapedSurrogateText(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"value":"\ud83d\ude80"}`,
		`{"value":"\\ud800"}`,
	} {
		if err := Validate([]byte(input), Limits{}); err != nil {
			t.Fatalf("Validate(%s): %v", input, err)
		}
	}
}

func TestValidateDepthAndNodeDefaultsRemainBounded(t *testing.T) {
	t.Parallel()
	tooDeep := strings.Repeat("[", defaultMaxDepth+1) +
		strings.Repeat("]", defaultMaxDepth+1)
	if err := Validate([]byte(tooDeep), Limits{}); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("deep Validate() error = %v, want %v", err, ErrTooDeep)
	}

	items := strings.Repeat("0,", defaultMaxNodes)
	tooMany := "[" + strings.TrimSuffix(items, ",") + "]"
	if err := Validate([]byte(tooMany), Limits{}); !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("large Validate() error = %v, want %v", err, ErrTooManyNodes)
	}
}
