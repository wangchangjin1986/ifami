package ingest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Recognizer 单张图片识别（方舟实现 + 测试替身）。
type Recognizer interface {
	Recognize(ctx context.Context, mime string, img []byte) (*ModelOutput, error)
}

// ModelOutput 模型输出 schema（ADR-005 推论 2）；schema 之外视为该图失败。
type ModelOutput struct {
	ImageKind string      `json:"image_kind"`
	Items     []ModelItem `json:"items"`
}

// ModelItem 模型识别出的一行。
type ModelItem struct {
	Title      string   `json:"title"`
	Weekdays   []int    `json:"weekdays"`
	Date       *string  `json:"date"`
	StartTime  string   `json:"start_time"`
	EndTime    string   `json:"end_time"`
	Until      *string  `json:"until"`
	Location   string   `json:"location"`
	Note       string   `json:"note"`
	ChildName  *string  `json:"child_name"`
	Confidence string   `json:"confidence"`
	Uncertain  []string `json:"uncertain"`
	SourceText string   `json:"source_text"`
	Unreadable bool     `json:"unreadable"`
}

const (
	kindCourseTable = "course_table"
	kindSchool      = "school_timetable"
	kindNotice      = "notice"
	kindOther       = "other"
)

// ArkClient 火山方舟 Chat Completions（OpenAI 兼容）直连，不引 SDK。
type ArkClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func NewArkClient(baseURL, apiKey, model string) *ArkClient {
	return &ArkClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 45 * time.Second},
	}
}

// Configured 方舟凭据与接入点是否齐全（未配置时 start 直接失败并提示手动录入）。
func (c *ArkClient) Configured() bool { return c.apiKey != "" && c.model != "" }

const systemPrompt = `你是家庭日程识别助手。用户上传一张图片（机构课表、学校作息/课程表、班级群通知截图、手写作息表等），请提取其中的日程安排。
只输出一个 JSON 对象，不要输出任何解释或 Markdown，格式严格如下：
{"image_kind":"course_table|school_timetable|notice|other",
 "items":[{"title":"课程或事项名","weekdays":[1-7 的整数，1=周一 7=周日；单次事项为空数组],
   "date":"单次事项的日期 YYYY-MM-DD，重复事项为 null","start_time":"HH:MM","end_time":"HH:MM 或空串",
   "until":"重复截止日期 YYYY-MM-DD（图上写了学期结束/截止日才填）或 null",
   "location":"地点或空串","note":"需携带物品等备注或空串","child_name":"图上出现的孩子姓名或 null",
   "confidence":"high|low","uncertain":["不确定的字段名，如 start_time"],
   "source_text":"该行在图中的原文片段","unreadable":false}]}
规则：
1. 学校课程表（按节次排列的课表、作息时间表）image_kind 用 school_timetable，每节课作为一个 item 输出并带真实上课时间。
2. 时间不明确（如"两点或三点"）取最可能的一个，confidence=low 并在 uncertain 中列出字段。
3. 看不清的行仍要输出：unreadable=true，source_text 写出能辨认的部分。
4. 年份缺失时按最近的未来日期推断；时间一律 24 小时制。
5. 图片与日程无关时 image_kind=other，items 为空数组。`

// Recognize 调用方舟识别单张图片。
func (c *ArkClient) Recognize(ctx context.Context, mime string, img []byte) (*ModelOutput, error) {
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
	reqBody := map[string]any{
		"model":       c.model,
		"temperature": 0.1,
		"messages": []any{
			map[string]any{"role": "system", "content": systemPrompt},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				map[string]any{"type": "text", "text": "请识别这张图片里的日程安排，今天是 " + time.Now().Format("2006-01-02") + "。"},
			}},
		},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// 只记录状态码与截断后的错误体，不回显请求内容（含图片）。
		return nil, fmt.Errorf("ark http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("ark 响应解析失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("ark 响应无 choices")
	}
	return parseModelOutput(out.Choices[0].Message.Content)
}

// parseModelOutput 解析模型文本为 schema；容忍 ```json 围栏与前后杂文本，其余一律视为失败。
func parseModelOutput(content string) (*ModelOutput, error) {
	s := strings.TrimSpace(content)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var mo ModelOutput
	if err := dec.Decode(&mo); err != nil {
		return nil, fmt.Errorf("模型输出不符合 schema: %w", err)
	}
	switch mo.ImageKind {
	case kindCourseTable, kindSchool, kindNotice, kindOther:
	default:
		return nil, fmt.Errorf("未知 image_kind: %q", mo.ImageKind)
	}
	return &mo, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
