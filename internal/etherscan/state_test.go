package etherscan

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"
)

type testStateProvider struct {
	native       map[string]string
	tokenBalance string
	tokenSupply  string
	err          error
	nativeCalls  [][]string
	balanceCall  []string
	supplyCall   string
}

func (provider *testStateProvider) NativeBalances(_ context.Context, addresses []string) ([]string, error) {
	provider.nativeCalls = append(provider.nativeCalls, append([]string(nil), addresses...))
	if provider.err != nil {
		return nil, provider.err
	}
	balances := make([]string, len(addresses))
	for index, address := range addresses {
		balances[index] = provider.native[address]
	}
	return balances, nil
}

func (provider *testStateProvider) ERC20Balance(_ context.Context, contract, owner string) (string, error) {
	provider.balanceCall = []string{contract, owner}
	return provider.tokenBalance, provider.err
}

func (provider *testStateProvider) ERC20TotalSupply(_ context.Context, contract string) (string, error) {
	provider.supplyCall = contract
	return provider.tokenSupply, provider.err
}

func TestNativeBalanceActionsUseAuthoritativeStateProvider(t *testing.T) {
	t.Parallel()
	provider := &testStateProvider{native: map[string]string{
		testSender:    "123",
		testRecipient: "456",
	}}
	backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1, State: provider})

	balance, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "balance", Values: url.Values{"address": {testSender}, "tag": {"latest"}},
	})
	if err != nil || balance != "123" {
		t.Fatalf("balance=%#v error=%v", balance, err)
	}
	multiple, err := backend.Execute(context.Background(), Request{
		Module: "account", Action: "balancemulti", Values: url.Values{"address": {testSender + "," + testRecipient}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(multiple)
	want := `[{"account":"0x52908400098527886E0F7030069857D2E4169EE7","balance":"123"},{"account":"0xde709f2102306220921060314715629080e2fb77","balance":"456"}]`
	if string(encoded) != want {
		t.Fatalf("multiple=%s want=%s", encoded, want)
	}
	wantCalls := [][]string{{testSender}, {testSender, testRecipient}}
	if !reflect.DeepEqual(provider.nativeCalls, wantCalls) {
		t.Fatalf("native calls=%v want=%v", provider.nativeCalls, wantCalls)
	}
}

func TestStateActionsRejectUnavailableMalformedAndUnsupportedTags(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		provider StateProvider
		request  Request
		want     error
	}{
		{
			name: "missing provider",
			request: Request{Module: "account", Action: "balance", Values: url.Values{
				"address": {testSender},
			}},
			want: ErrStateUnavailable,
		},
		{
			name:     "provider failure",
			provider: &testStateProvider{err: errors.New("archive RPC failed")},
			request: Request{Module: "account", Action: "balance", Values: url.Values{
				"address": {testSender},
			}},
			want: ErrStateUnavailable,
		},
		{
			name:     "malformed quantity",
			provider: &testStateProvider{native: map[string]string{testSender: "01"}},
			request: Request{Module: "account", Action: "balance", Values: url.Values{
				"address": {testSender},
			}},
			want: ErrStateUnavailable,
		},
		{
			name:     "unsupported tag",
			provider: &testStateProvider{native: map[string]string{testSender: "1"}},
			request: Request{Module: "account", Action: "balance", Values: url.Values{
				"address": {testSender}, "tag": {"pending"},
			}},
			want: ErrInvalidParameter,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1, State: test.provider})
			_, err := backend.Execute(context.Background(), test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}
