package server

import (
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chendefine/go-coze"
	"github.com/gin-gonic/gin"
)

const (
	ObjectChatCompletion      = "chat.completion"
	ObjectChatCompletionChunk = "chat.completion.chunk"

	RoleSystem       = "system"
	FinishReasonStop = "stop"

	chatIdPrefix = "chatcmpl-%s"
	sseDone      = "[DONE]"
)

var (
	errIncorrectToken  = CompletionsError{Error: ErrorWrap{Code: "invalid_api_key", Message: "Incorrect API key provided."}}
	errModelNotFound   = CompletionsError{Error: ErrorWrap{Code: "model_not_found", Message: "The model `%s` does not exist or you do not have access to it."}}
	errInvalidJsonBody = CompletionsError{Error: ErrorWrap{Code: "invalid_request_error", Message: "We could not parse the JSON body of your request. (HINT: This likely means you aren't using your HTTP library correctly. The OpenAI API expects a JSON payload, but what was sent was not valid JSON. If you have trouble figuring out how to fix this, please contact us through our help center at help.openai.com."}}
)

type MessageWrap struct {
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"`
}

type ChoiceWrap struct {
	Index   int          `json:"index"`
	Delta   *MessageWrap `json:"delta,omitempty"`
	Message *MessageWrap `json:"message,omitempty"`
	// Logprobs     any         `json:"logprobs"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type UsageWrap struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type CompletionsReq struct {
	Model    string         `json:"model"`
	Messages []*MessageWrap `json:"messages"`
	Stream   bool           `json:"stream"`
}

type CompletionsRsp struct {
	Id      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []*ChoiceWrap `json:"choices"`
	Usage   *UsageWrap    `json:"usage,omitempty"`
	// SystemFingerprint any           `json:"system_fingerprint"`
}

type ErrorWrap struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type CompletionsError struct {
	Error ErrorWrap `json:"error"`
}

func (s *Server) Completions(ctx *gin.Context) {
	// verify token
	if len(s.auths) > 0 {
		token := strings.TrimPrefix(ctx.GetHeader("Authorization"), "Bearer ")
		if _, ok := s.auths[token]; !ok {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errIncorrectToken)
			return
		}
	}

	req := new(CompletionsReq)
	err := ctx.BindJSON(req)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, errInvalidJsonBody)
		return
	}

	// pick bot by model
	var bot *coze.Bot
	if ids, ok := s.models[req.Model]; !ok && len(s.models) > 1 {
		err := errModelNotFound
		err.Error.Message = fmt.Sprintf(err.Error.Message, req.Model)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, err)
		return
	} else if len(s.models) == 1 {
		ids = s.models[""]
		bot = s.bots[ids[rand.Intn(len(ids))]]
	} else {
		bot = s.bots[ids[rand.Intn(len(ids))]]
	}

	// make coze chat request
	chat, err := bot.Chat(ctx, makeCozeChatReq(req))
	if err != nil {
		e, _ := err.(*coze.ErrorWrap)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, CompletionsError{Error: ErrorWrap{Code: strconv.Itoa(e.Code), Message: e.Msg}})
		return
	}

	if req.Stream { // handle stream request
		handleCompletionsStream(ctx, chat, req.Model)

	} else { // handle no stream request
		parseCompletionsReturn(ctx, chat, req.Model)
	}

}

func handleCompletionsStream(c *gin.Context, chat *coze.ChatRsp, model string) {
	// write sse headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("Cache-Control", "no-cache")

	stream := chat.Stream()
	for {
		select {
		case <-c.Done():
			c.Done()
			return

		case ev, ok := <-stream:
			if !ok {
				c.Done()
				return
			}

			switch ev.Event {
			case coze.ChatInProgress:
				// first message with role and without content
				msg := makeOpenAiChatChunk(ev.Data, model)
				msg.Id = fmt.Sprintf(chatIdPrefix, ev.Data.Id)
				msg.Choices[0].Delta = &MessageWrap{Role: coze.RoleAssistant, Content: ""}
				c.SSEvent("", msg)

			case coze.MessageDelta:
				// message with content
				c.SSEvent("", makeOpenAiChatChunk(ev.Data, model))

			case coze.ChatCompleted:
				// last message with finish_reason and without delta
				msg := makeOpenAiChatChunk(ev.Data, model)
				msg.Choices[0].FinishReason = FinishReasonStop
				msg.Choices[0].Delta = &MessageWrap{}
				c.SSEvent("", msg)

				// finish signal done
				c.SSEvent("", sseDone)
				c.Done()
				return
			}
		}
	}
}

func parseCompletionsReturn(c *gin.Context, chat *coze.ChatRsp, model string) {
	result := chat.GetResult(c.Request.Context())
	if result.LastError != nil {
		err := CompletionsError{Error: ErrorWrap{Code: strconv.Itoa(result.LastError.Code), Message: result.LastError.Msg}}
		c.AbortWithStatusJSON(http.StatusInternalServerError, err)
		return
	}
	choices := []*ChoiceWrap{{Message: &MessageWrap{Role: coze.RoleAssistant, Content: result.Answer}, FinishReason: FinishReasonStop}}
	usage := &UsageWrap{PromptTokens: result.Usage.InputCount, CompletionTokens: result.Usage.OutputCount, TotalTokens: result.Usage.TokenCount}
	rsp := &CompletionsRsp{Id: fmt.Sprintf(chatIdPrefix, result.ChatId), Object: ObjectChatCompletion, Created: time.Now().Unix(), Model: model, Choices: choices, Usage: usage}
	c.JSON(http.StatusOK, rsp)
}

func transOpenAiMessageContent(role string, content any) *coze.ChatMessage {
	if str, ok := content.(string); ok {
		return &coze.ChatMessage{Role: role, Content: str, ContentType: coze.ContentTypeText}
	} else if objs, ok := content.([]any); ok {
		items := make([]*coze.ContentItem, 0, len(objs))
		for _, obj := range objs {
			objm, ok := obj.(map[string]any)
			if !ok {
				continue
			}

			typ, ok := objm["type"].(string)
			if !ok {
				continue
			} else if typ != "text" && typ != "image_url" {
				continue
			}

			switch typ {
			case "text":
				text, _ := objm["text"].(string)
				items = append(items, &coze.ContentItem{Type: coze.ContentItemTypeText, Text: text})
			case "image_url":
				imgm, ok := objm["image_url"].(map[string]any)
				if !ok {
					continue
				}
				imgurl, ok := imgm["url"].(string)
				if !ok {
					continue
				}
				items = append(items, &coze.ContentItem{Type: coze.ContentItemTypeImage, FileUrl: imgurl})
			}
		}
		return &coze.ChatMessage{Role: role, Content: items, ContentType: coze.ContentTypeObjectString}
	}
	return nil
}

func mergeSystemPromptAndUserMessage(sysp, msgs []*coze.ChatMessage) {
	if len(sysp) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == coze.RoleUser {
				userm := msgs[i]
				var typeText bool
				if msgs[i].ContentType == coze.ContentTypeText {
					typeText = true
					for _, syspm := range sysp {
						if !typeText {
							break
						} else if _, ok := syspm.Content.(string); ok {
							continue
						} else if items, ok := syspm.Content.([]*coze.ContentItem); ok {
							for _, item := range items {
								if item.Type != coze.ContentItemTypeText {
									typeText = false
									break
								}
							}
						}
					}
				}

				if typeText {
					buff := strings.Builder{}

					for _, syspm := range sysp {
						if str, ok := syspm.Content.(string); ok {
							buff.WriteString(str)
							buff.WriteByte('\n')
						} else if items, ok := syspm.Content.([]*coze.ContentItem); ok {
							for _, item := range items {
								buff.WriteString(item.Text)
								buff.WriteByte('\n')
							}
						}
					}

					buff.WriteString(userm.Content.(string))
					userm.Content = buff.String()
				} else {
					buff := strings.Builder{}
					contents := []*coze.ContentItem{{Type: coze.ContentTypeText}}

					for _, syspm := range sysp {
						if str, ok := syspm.Content.(string); ok {
							buff.WriteString(str)
							buff.WriteByte('\n')
						} else if items, ok := syspm.Content.([]*coze.ContentItem); ok {
							for _, item := range items {
								if item.Type == coze.ContentItemTypeText {
									buff.WriteString(item.Text)
									buff.WriteByte('\n')
								} else if item.Type == coze.ContentItemTypeImage {
									contents = append(contents, item)
								}
							}
						}
					}

					if str, ok := userm.Content.(string); ok {
						buff.WriteString(str)
					} else if items, ok := userm.Content.([]*coze.ContentItem); ok {
						for _, item := range items {
							if item.Type == coze.ContentItemTypeText {
								buff.WriteString(item.Text)
								buff.WriteByte('\n')
							} else if item.Type == coze.ContentItemTypeImage {
								contents = append(contents, item)
							}
						}
					}

					contents[0].Text = buff.String()
					userm.ContentType, userm.Content = coze.ContentTypeObjectString, contents
				}
				break
			}
		}
	}
}

func makeCozeChatReq(req *CompletionsReq) *coze.ChatReq {
	msgs := make([]*coze.ChatMessage, 0, len(req.Messages))
	sysp := []*coze.ChatMessage{}
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			sysp = append(sysp, transOpenAiMessageContent(msg.Role, msg.Content))
		} else if msg.Role == coze.RoleUser || msg.Role == coze.RoleAssistant {
			msgs = append(msgs, transOpenAiMessageContent(msg.Role, msg.Content))
		}
	}
	mergeSystemPromptAndUserMessage(sysp, msgs)
	return &coze.ChatReq{Stream: true, AdditionalMessages: msgs}
}

func makeOpenAiChatChunk(ev *coze.ChatEventData, model string) *CompletionsRsp {
	choices := []*ChoiceWrap{{Delta: &MessageWrap{ /* Role: coze.RoleAssistant, */ Content: ev.Content}}}
	return &CompletionsRsp{Id: fmt.Sprintf(chatIdPrefix, ev.ChatId), Object: ObjectChatCompletionChunk, Created: time.Now().Unix(), Model: model, Choices: choices}
}
