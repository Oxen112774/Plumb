package voice

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// answering starts a stub endpoint that replies with content, and fails the
// test if the request is not the one the vendor contract requires.
func answering(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		var req chatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request is not the documented shape: %v", err)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" {
			t.Fatalf("messages = %+v, want a system prompt then the digest", req.Messages)
		}
		if !strings.Contains(req.Messages[0].Content, "不许编") {
			t.Error("the system prompt does not forbid inventing numbers")
		}
		if !strings.Contains(req.Messages[1].Content, "mysqld") {
			t.Errorf("the digest did not reach the model: %q", req.Messages[1].Content)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := chatResponse{}
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = content
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

const testDigest = "主机 prod-db-01，4 核\n判定：\n- 磁盘 IO（严重）：磁盘让整机停摆\n" +
	"  进程 mysqld PID 1234：读 40.1 MiB/s\n"

func testModel(t *testing.T, srv *httptest.Server) *Model {
	t.Helper()
	return &Model{URL: srv.URL, Key: "test-key", Name: "test", HTTP: srv.Client()}
}

func TestParagraphAcceptsAGroundedAnswer(t *testing.T) {
	srv := answering(t, "磁盘这边堵住了，主要是 PID 1234 在写，40.1 MiB/s 的量级。")
	defer srv.Close()

	got, err := testModel(t, srv).ParagraphFor(testDigest)
	if err != nil {
		t.Fatalf("ParagraphFor: %v", err)
	}
	if !strings.Contains(got, "1234") {
		t.Errorf("answer = %q", got)
	}
}

func TestParagraphRejectsAnInventedNumber(t *testing.T) {
	srv := answering(t, "磁盘这边堵住了，PID 9999 是元凶。")
	defer srv.Close()

	if _, err := testModel(t, srv).ParagraphFor(testDigest); err == nil {
		t.Fatal("an answer citing a PID that is not in the data was accepted")
	}
}

func TestParagraphRejectsACodeBlock(t *testing.T) {
	srv := answering(t, "```\n磁盘堵了\n```")
	defer srv.Close()

	if _, err := testModel(t, srv).ParagraphFor(testDigest); err == nil {
		t.Fatal("an answer wrapped in a code block was accepted")
	}
}

func TestParagraphSurfacesUpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()

	if _, err := testModel(t, srv).ParagraphFor(testDigest); err == nil {
		t.Fatal("a 429 was treated as an answer")
	}
}

func TestParagraphRejectsUnparseableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>login required</html>")
	}))
	defer srv.Close()

	if _, err := testModel(t, srv).ParagraphFor(testDigest); err == nil {
		t.Fatal("an HTML body was treated as an answer")
	}
}

func TestParagraphRefusesAnEmptyDigest(t *testing.T) {
	srv := answering(t, "无所谓，不该被调用")
	defer srv.Close()

	if _, err := testModel(t, srv).ParagraphFor("   "); err == nil {
		t.Fatal("an empty digest was sent to the model")
	}
}

// Write is the boundary that matters: whatever the model does, the caller gets
// a complete message. This is the degradation contract.
func TestWriteFallsBackWhenTheModelFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var withModel, without strings.Builder
	if err := Write(&withModel, sampleReport(), false, testModel(t, srv)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Write(&without, sampleReport(), false, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if withModel.String() != without.String() {
		t.Errorf("a failed model changed the message:\n--- with ---\n%s\n--- without ---\n%s",
			withModel.String(), without.String())
	}
}

func TestWriteIncludesAGroundedModelParagraph(t *testing.T) {
	srv := answering(t, "磁盘这边堵住了，主要是 PID 1234 在写。")
	defer srv.Close()

	var b strings.Builder
	if err := Write(&b, sampleReport(), false, testModel(t, srv)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := b.String()

	if !strings.Contains(out, "〔模型综合〕") {
		t.Error("a model-written paragraph must say that it is one")
	}
	if !strings.Contains(out, "磁盘这边堵住了，主要是 PID 1234 在写。") {
		t.Errorf("the model paragraph is missing:\n%s", out)
	}
	// The hard facts still come from the report, not from the model. The line
	// may wrap between the number and its unit, so the numbers are checked
	// separately from the phrase.
	for _, want := range []string{"40.1", "976.6", "mysqld（PID 1234）"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report's own facts are missing %q:\n%s", want, out)
		}
	}
}

func TestFromEnvRequiresConfiguration(t *testing.T) {
	t.Setenv(envURL, "")
	t.Setenv(envModel, "")

	if m, err := FromEnv(false); m != nil || err != nil {
		t.Errorf("FromEnv(false) = (%v, %v), want (nil, nil)", m, err)
	}
	if _, err := FromEnv(true); err == nil {
		t.Error("asking for -ai with nothing configured must not pass silently")
	}

	t.Setenv(envURL, "https://example.invalid/v1/chat/completions")
	t.Setenv(envModel, "some-model")
	m, err := FromEnv(true)
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if m == nil || m.URL == "" || m.Name != "some-model" {
		t.Errorf("model = %+v", m)
	}
}
