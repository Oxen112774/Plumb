package voice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
)

// Model is an optional language model that reads a whole diagnosis and writes
// the one paragraph a table cannot: which of these findings are the same story.
//
// It is optional in the strongest sense. The message is complete without it, and
// every failure — no key, no network, a timeout, an answer that invents a number
// — degrades to exactly the text the table would have produced. Nothing about
// the diagnosis depends on this file.
type Model struct {
	// URL is the full chat-completions endpoint, given rather than guessed.
	// Constructing it from a base URL means guessing the vendor's path layout,
	// and a wrong guess fails as a confusing 404 in the middle of an incident.
	URL   string
	Key   string
	Name  string
	HTTP  *http.Client
	Notes io.Writer // where the "this leaves the machine" warning goes; nil = silent
}

// aiTimeout bounds the whole request. It is deliberately short: this runs while
// someone is waiting to find out whether they have to get up, and a paragraph
// of prose is never worth making them wait longer than the diagnosis itself.
const aiTimeout = 12 * time.Second

// maxParagraph bounds the answer. The prompt asks for one short paragraph; an
// answer several times that is a model that ignored the instructions, and an
// ignored instruction about length is not a good sign for the ones about not
// inventing numbers.
const maxParagraph = 400

// Endpoint environment variables.
const (
	envURL   = "PLUMB_AI_URL"
	envKey   = "PLUMB_AI_KEY"
	envModel = "PLUMB_AI_MODEL"
)

// FromEnv builds the client the environment describes.
//
// It returns (nil, nil) when the feature was not asked for, so that a machine
// with no model configured is not an error. It returns an error only when the
// feature was asked for and cannot be honoured, because silently dropping a
// flag the user typed is worse than saying it did not take effect.
func FromEnv(enabled bool) (*Model, error) {
	if !enabled {
		return nil, nil
	}
	url := strings.TrimSpace(os.Getenv(envURL))
	if url == "" {
		return nil, fmt.Errorf("-ai 需要 %s（完整的 chat/completions 地址）", envURL)
	}
	name := strings.TrimSpace(os.Getenv(envModel))
	if name == "" {
		return nil, fmt.Errorf("-ai 需要 %s", envModel)
	}
	return &Model{
		URL:   url,
		Key:   strings.TrimSpace(os.Getenv(envKey)),
		Name:  name,
		HTTP:  &http.Client{Timeout: aiTimeout},
		Notes: os.Stderr,
	}, nil
}

// warnOnce reports where the data is about to go.
//
// A diagnosis names processes and file paths. That is a description of someone's
// infrastructure, and it does not leave the machine unless its owner says so —
// so it is said out loud rather than buried in a manual.
func (m *Model) warnOnce() {
	if m.Notes == nil {
		return
	}
	fmt.Fprintf(m.Notes,
		"plumb: 本次诊断的进程名、判定与文件路径会发送给 %s；不要的话去掉 -ai\n",
		hostOf(m.URL))
}

// note writes an operational remark to the same place the warning goes. It never
// touches stdout: what a model did or failed to do is not part of the diagnosis.
func (m *Model) note(format string, args ...any) {
	if m.Notes == nil {
		return
	}
	fmt.Fprintf(m.Notes, "plumb: "+format+"\n", args...)
}

// hostOf returns just the host part of a URL, for a message that should not
// print a path or a query string that may hold a key.
func hostOf(url string) string {
	s := url
	if i := strings.Index(s, "//"); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Digest renders a report as the text the model is allowed to draw on.
//
// Command lines are deliberately left out. argv routinely carries passwords,
// connection strings and tokens, and unlike everything else here it is not
// something the diagnosis needed in the first place.
func Digest(r diagnose.Report) string {
	var b strings.Builder

	name := r.System.Hostname
	if name == "" {
		name = "未知主机"
	}
	fmt.Fprintf(&b, "主机 %s，%d 核，load1 %.2f\n",
		name, r.System.Cores, r.System.Load[0])

	if !r.SingleSample {
		fmt.Fprintf(&b, "CPU 繁忙 %.1f%%，等磁盘 %.1f%%，被抢占 %.1f%%，空闲 %.1f%%\n",
			r.CPUUsage.Busy, r.CPUUsage.IOWait, r.CPUUsage.Steal, r.CPUUsage.Idle)
	}
	var hot []string
	for _, c := range r.Cores {
		if c.Busy >= 80 {
			hot = append(hot, fmt.Sprintf("CPU%d %.0f%%", c.ID, c.Busy))
		}
	}
	if len(hot) > 0 {
		fmt.Fprintf(&b, "跑满的核心 %s（共 %d 个核心）\n",
			strings.Join(hot, " "), len(r.Cores))
	}
	if r.Memory.TotalKB > 0 {
		fmt.Fprintf(&b, "内存 已用 %.1f%%，交换 %.1f%%\n",
			r.Memory.UsedPercent(), r.Memory.SwapPercent())
	}
	if r.Pressure.Supported {
		fmt.Fprintf(&b, "压力 cpu some %.2f%%，io full %.2f%%，memory full %.2f%%\n",
			r.Pressure.CPU.Some.Avg10,
			r.Pressure.IO.Full.Avg10,
			r.Pressure.Memory.Full.Avg10)
	}
	if r.Threads.Total > 0 {
		fmt.Fprintf(&b, "线程 共 %d 个，处于 D 状态 %d 个\n",
			r.Threads.Total, r.Threads.Blocked)
	}

	b.WriteString("判定：\n")
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "- %s（%s）：%s\n", f.Area, f.Severity.Label(), f.Headline)
		for _, c := range f.Culprits {
			fmt.Fprintf(&b, "  进程 %s PID %d", c.Name, c.PID)
			if c.User != "" {
				fmt.Fprintf(&b, " 用户 %s", c.User)
			}
			if note := strings.Join(strings.Fields(c.Note), " "); note != "" {
				fmt.Fprintf(&b, "：%s", note)
			}
			b.WriteString("\n")
		}
	}
	if len(r.Errors) > 0 {
		fmt.Fprintf(&b, "未能测到：%s\n", strings.Join(r.Errors, "；"))
	}
	return b.String()
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

const systemPrompt = `你是服务器诊断工具 plumb 的一句话层。你收到一份已经确定的技术结论，把它读成一段人话。

硬规则，违反任何一条这份回答就作废：
1. 只使用给你的数据。禁止引入任何新的数字、进程名、文件名、端口 —— 一个都不许编。
2. 不许说“不用担心”“一切正常”“我们正在处理”这类话。plumb 是只读工具，它什么都没做，你也没有资格替它承诺。
3. 不许软化事实。该急就说急。
4. 只输出一段话，不超过 120 字。不要列表、标题、Markdown、emoji、代码块。
5. 用中文。`

// ParagraphFor asks the model for the synthesis paragraph.
//
// The digest is the only material it is given, and the answer is checked against
// it before use: any number of two or more digits that does not appear in the
// digest means the answer is discarded. That check exists because a plausible
// invented PID is the single most damaging thing this feature could print — it
// is exactly the kind of detail a tired operator would act on.
func (m *Model) ParagraphFor(digest string) (string, error) {
	if strings.TrimSpace(digest) == "" {
		return "", errors.New("nothing to summarise")
	}

	body, err := json.Marshal(chatRequest{
		Model: m.Name,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: digest},
		},
		Temperature: 0.3,
		MaxTokens:   400,
	})
	if err != nil {
		return "", err
	}

	m.warnOnce()

	req, err := http.NewRequest(http.MethodPost, m.URL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.Key != "" {
		req.Header.Set("Authorization", "Bearer "+m.Key)
	}

	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: aiTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read with a cap: a runaway response should not be able to fill memory,
	// and anything past the cap is already longer than the answer may be.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("模型返回 %s", resp.Status)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("看不懂模型的返回：%w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("模型报错：%s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("模型没有返回内容")
	}

	return sanitize(parsed.Choices[0].Message.Content, digest)
}

// sanitize turns a raw answer into something safe to print, or rejects it.
func sanitize(raw, source string) (string, error) {
	out := strings.TrimSpace(raw)
	if out == "" {
		return "", errors.New("模型返回空内容")
	}
	// A model that answered in a code block or with a heading ignored the
	// instructions; that is enough doubt to drop the answer.
	if strings.Contains(out, "```") {
		return "", errors.New("模型用了代码块")
	}
	out = strings.Join(strings.Fields(out), " ")
	if n := len([]rune(out)); n > maxParagraph {
		return "", fmt.Errorf("模型写了 %d 个字，超过 %d", n, maxParagraph)
	}
	if bad := unknownNumbers(out, source); len(bad) > 0 {
		return "", fmt.Errorf("模型引入了报告里没有的数字：%s", strings.Join(bad, "、"))
	}
	return out, nil
}

// unknownNumbers returns the multi-digit numbers in text that do not appear in
// source.
//
// Single digits are left alone: "1 个线程" is counting, not a measurement. Two or
// more digits is a claim about this machine, and if it is not in the data the
// model was given, it came from somewhere other than this machine.
func unknownNumbers(text, source string) []string {
	var bad []string
	for _, tok := range numbersIn(text) {
		if len(strings.ReplaceAll(tok, ".", "")) < 2 {
			continue
		}
		// Substring rather than exact equality: "40" is grounded when the data
		// says "40.1 MiB/s", and rejecting that would reject the answer a
		// careful model actually writes.
		if !strings.Contains(source, tok) {
			bad = append(bad, tok)
		}
	}
	return bad
}

// numbersIn returns the maximal runs of digits and decimal points in s.
func numbersIn(s string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r == '.':
			cur.WriteRune(r)
		default:
			if cur.Len() > 0 {
				out = append(out, strings.Trim(cur.String(), "."))
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		out = append(out, strings.Trim(cur.String(), "."))
	}
	// Drop empties left by a run that was nothing but dots.
	kept := out[:0]
	for _, t := range out {
		if t != "" {
			kept = append(kept, t)
		}
	}
	return kept
}
