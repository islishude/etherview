package httpapi

import (
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/config"
)

func TestEnabledCapabilityModulesRejectMissingDependencies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		configure   func(*Options)
		wantMessage string
	}{
		{
			name: "native", wantMessage: "native capability",
			configure: func(options *Options) { options.Requirements.Native = true },
		},
		{
			name: "catalog", wantMessage: "catalog reader is required",
			configure: func(options *Options) { options.Requirements.Catalog = true },
		},
		{
			name: "analytics", wantMessage: "analytics reader is required",
			configure: func(options *Options) { options.Requirements.Analytics = true },
		},
		{
			name: "compatibility", wantMessage: "Etherscan compatibility handler is required",
			configure: func(options *Options) { options.Requirements.Compatibility = true },
		},
		{
			name: "events", wantMessage: "event broker is required",
			configure: func(options *Options) { options.Requirements.Events = true },
		},
		{
			name: "home snapshots", wantMessage: "home snapshot source is required",
			configure: func(options *Options) { options.Requirements.HomeSnapshots = true },
		},
		{
			name: "proxy", wantMessage: "proxy reader is required",
			configure: func(options *Options) { options.Requirements.Proxy = true },
		},
		{
			name: "web", wantMessage: "web handler is required",
			configure: func(options *Options) { options.Requirements.Web = true },
		},
		{
			name: "metadata", wantMessage: "metadata reader is required",
			configure: func(options *Options) {
				options.Config.Features.NFTMetadata = true
				options.Requirements.Metadata = true
			},
		},
		{
			name: "verification", wantMessage: "verification reader is required",
			configure: func(options *Options) {
				options.Config.Features.Verification = true
				options.Requirements.Verification = true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := Options{Config: config.Default(), Reader: fakeReader{}}
			test.configure(&options)
			if _, err := New(options); err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("error=%v, want %q", err, test.wantMessage)
			}
		})
	}
}

func TestCapabilityDependencyErrorsAreDeterministic(t *testing.T) {
	t.Parallel()
	options := Options{
		Config: config.Default(), Reader: fakeReader{},
		Requirements: CapabilityRequirements{Native: true},
	}
	_, first := New(options)
	_, second := New(options)
	if first == nil || second == nil || first.Error() != second.Error() {
		t.Fatalf("first=%v second=%v", first, second)
	}
}
