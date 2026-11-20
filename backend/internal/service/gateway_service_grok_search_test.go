//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newGrokSearchTestService(respBody string) (*GatewayService, *httpUpstreamRecorder) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}}
	return &GatewayService{httpUpstream: upstream}, upstream
}

// cli-chat-proxy OAuth account (empty base_url => official CLI gateway).
func newGrokCLIProxyTestAccount() *Account {
	return &Account{
		ID:       42,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "grok-access-token",
		},
	}
}

const grokSearchRequestBody = `{"model":"grok-4.3","input":"search for news","tools":[{"type":"web_search"}],"include":["web_search_call.action.sources"],"store":false,"stream":false}`

// Response shape produced by cli-chat-proxy when it silently drops web_search:
// a plain message with model-fabricated URLs and no search-call output item.
const grokSearchIgnoredResponse = `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"results\":[{\"url\":\"https://example.com\",\"title\":\"made up\",\"snippet\":\"...\"}]}"}]}]}`

const grokSearchRealResponse = `{"output":[{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"news","sources":[{"url":"https://example.com","title":"Example"}]}},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"results\":[{\"url\":\"https://example.com\",\"title\":\"Example\",\"snippet\":\"...\"}]}"}]}]}`

func TestDoGrokNativeResponsesJSON_CLIProxyIgnoredWebSearchFailsOver(t *testing.T) {
	svc, upstream := newGrokSearchTestService(grokSearchIgnoredResponse)

	_, err := svc.DoGrokNativeResponsesJSON(context.Background(), newGrokCLIProxyTestAccount(), []byte(grokSearchRequestBody))
	require.Error(t, err)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Equal(t, GatewayFailureReason("grok_search_tool_ignored"), failoverErr.Reason)
	require.Equal(t, "https://cli-chat-proxy.grok.com/v1/responses", upstream.lastReq.URL.String())
}

func TestDoGrokNativeResponsesJSON_CLIProxyRealSearchPasses(t *testing.T) {
	svc, upstream := newGrokSearchTestService(grokSearchRealResponse)

	respBytes, err := svc.DoGrokNativeResponsesJSON(context.Background(), newGrokCLIProxyTestAccount(), []byte(grokSearchRequestBody))
	require.NoError(t, err)
	require.Contains(t, string(respBytes), "web_search_call")
	require.Equal(t, HTTPUpstreamProfileGrok, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
}

func TestDoGrokNativeResponsesJSON_CLIProxyNoSearchToolNotChecked(t *testing.T) {
	svc, _ := newGrokSearchTestService(grokSearchIgnoredResponse)

	// No tools in the request: the ignored-tool guard must not fire.
	respBytes, err := svc.DoGrokNativeResponsesJSON(context.Background(), newGrokCLIProxyTestAccount(), []byte(`{"model":"grok-4.3","input":"hello"}`))
	require.NoError(t, err)
	require.NotEmpty(t, respBytes)
}

func TestDoGrokNativeResponsesJSON_NonCLIProxyEmptySourcesPass(t *testing.T) {
	// api.x.ai-keyed accounts are not held to the cli-proxy search-call check;
	// a genuine upstream empty result stays a success.
	account := newGrokCLIProxyTestAccount()
	account.Credentials["base_url"] = "https://api.x.ai/v1"
	svc, upstream := newGrokSearchTestService(grokSearchIgnoredResponse)

	respBytes, err := svc.DoGrokNativeResponsesJSON(context.Background(), account, []byte(grokSearchRequestBody))
	require.NoError(t, err)
	require.NotEmpty(t, respBytes)
	require.Equal(t, "https://api.x.ai/v1/responses", upstream.lastReq.URL.String())
}
