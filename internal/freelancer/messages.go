package freelancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ThreadListOptions filters inbox queries.
type ThreadListOptions struct {
	Threads []int64
	// Folders are inbox, sent, freelancer, archived, requests, none.
	Folders []string
	// UnreadOnly restricts to threads with unread messages.
	UnreadOnly bool
	Limit      int
	Offset     int
}

// Threads lists message threads with the last message and unread counters.
func (c *Client) Threads(ctx context.Context, opts ThreadListOptions) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	query := idList("threads", opts.Threads)
	for _, folder := range opts.Folders {
		query.Add("folders[]", folder)
	}
	if opts.UnreadOnly {
		query.Set("is_read", "false")
	}
	query.Set("limit", strconv.Itoa(limitOr(opts.Limit, 20)))
	if opts.Offset > 0 {
		query.Set("offset", strconv.Itoa(opts.Offset))
	}
	query.Set("last_message", "true")
	query.Set("unread_count", "true")
	query.Set("context_details", "true")
	query.Set("user_details", "true")
	return c.API(ctx, http.MethodGet, "/messages/0.1/threads/", query, nil)
}

// Messages reads the history of one thread, newest first.
func (c *Client) Messages(ctx context.Context, threadID int64, limit, offset int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if threadID == 0 {
		return nil, errors.New("thread id is required")
	}
	query := idList("threads", []int64{threadID})
	query.Set("limit", strconv.Itoa(limitOr(limit, 30)))
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	query.Set("thread_details", "true")
	query.Set("user_details", "true")
	return c.API(ctx, http.MethodGet, "/messages/0.1/messages/", query, nil)
}

// SearchMessages finds messages inside a thread.
func (c *Client) SearchMessages(ctx context.Context, threadID int64, query string, limit int) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("thread_id", strconv.FormatInt(threadID, 10))
	values.Set("query", query)
	values.Set("limit", strconv.Itoa(limitOr(limit, 20)))
	return c.API(ctx, http.MethodGet, "/messages/0.1/messages/search/", values, nil)
}

// SendMessage posts a message, optionally with attachments, into a thread.
func (c *Client) SendMessage(ctx context.Context, threadID int64, text string, files []FileUpload) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if threadID == 0 {
		return nil, errors.New("thread id is required")
	}
	if text == "" && len(files) == 0 {
		return nil, errors.New("message text or an attachment is required")
	}
	form := url.Values{}
	form.Set("message", text)
	form.Set("client_message_id", strconv.FormatInt(time.Now().UnixMilli(), 10))
	form.Set("source", "21")
	for i := range files {
		if files[i].Field == "" {
			files[i].Field = "files[]"
		}
	}
	return c.DoRaw(ctx, Request{
		Method: http.MethodPost,
		Path:   fmt.Sprintf("/messages/0.1/threads/%d/messages_new/", threadID),
		Form:   form,
		Files:  files,
	})
}

// NewThread describes a conversation to start.
type NewThread struct {
	// Members are the other participants; the account is added automatically.
	Members []int64
	// ContextType is project, contest, group, or none.
	ContextType string
	ContextID   int64
	Message     string
}

// CreateThread opens a conversation. Freelancer rejects unsolicited messages to
// users with no shared project context.
func (c *Client) CreateThread(ctx context.Context, in NewThread) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if len(in.Members) == 0 {
		return nil, errors.New("at least one member id is required")
	}
	if in.ContextType == "" {
		in.ContextType = "none"
	}
	form := url.Values{}
	form.Set("thread_type", threadTypeFor(in.ContextType))
	form.Set("context_type", in.ContextType)
	if in.ContextType != "none" && in.ContextID != 0 {
		form.Set("context", strconv.FormatInt(in.ContextID, 10))
	}
	for _, member := range in.Members {
		form.Add("members[]", strconv.FormatInt(member, 10))
	}
	form.Set("message", in.Message)
	form.Set("source", "21")
	return c.DoRaw(ctx, Request{
		Method: http.MethodPost,
		Path:   "/messages/0.1/threads/",
		Form:   form,
	})
}

func threadTypeFor(contextType string) string {
	if contextType == "none" || contextType == "" {
		return "private_chat"
	}
	return "primary"
}

// Thread actions accepted by PUT /messages/0.1/threads/.
const (
	ThreadActionRead    = "read"
	ThreadActionMute    = "mute"
	ThreadActionUnmute  = "unmute"
	ThreadActionBlock   = "block"
	ThreadActionUnblock = "unblock"
	ThreadActionStar    = "star"
	ThreadActionUnstar  = "unstar"
	ThreadActionArchive = "archive"
)

// ThreadActions lists the accepted thread actions.
func ThreadActions() []string {
	return []string{
		ThreadActionRead, ThreadActionMute, ThreadActionUnmute, ThreadActionBlock,
		ThreadActionUnblock, ThreadActionStar, ThreadActionUnstar, ThreadActionArchive,
	}
}

// ThreadAction applies an action to threads, e.g. marking them read. The
// endpoint reads the payload as form fields; a JSON body is rejected with
// "Missing required parameter 'action'".
func (c *Client) ThreadAction(ctx context.Context, threadIDs []int64, action string) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	if len(threadIDs) == 0 {
		return nil, errors.New("at least one thread id is required")
	}
	if action == "" {
		action = ThreadActionRead
	}
	form := url.Values{"action": {action}}
	for _, id := range threadIDs {
		form.Add("threads[]", strconv.FormatInt(id, 10))
	}
	return c.DoRaw(ctx, Request{
		Method: http.MethodPut,
		Path:   "/messages/0.1/threads/",
		Form:   form,
	})
}

// ThreadAttachments lists files shared in a thread.
func (c *Client) ThreadAttachments(ctx context.Context, threadID int64) (json.RawMessage, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	return c.API(ctx, http.MethodGet, fmt.Sprintf("/messages/0.1/threads/%d/attachments/", threadID), nil, nil)
}
