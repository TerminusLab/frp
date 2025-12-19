package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/server/helper"
)

type CardTemplate string

const (
	TemplateRed    CardTemplate = "red"    // Error / Alert (default for failures)
	TemplateBlue   CardTemplate = "blue"   // Info / Normal message
	TemplateGreen  CardTemplate = "green"  // Success
	TemplateYellow CardTemplate = "yellow" // Warning
	TemplatePurple CardTemplate = "purple" // Other / custom
)

// msgRecord stores the details of messages pending aggregation.
type msgRecord struct {
	title     string
	content   string
	note      string
	template  CardTemplate
	count     int32 // atomic counter for thread-safe incrementing
	firstSeen time.Time
}

var (
	// aggregationMap stores key -> *msgRecord
	aggregationMap sync.Map
)

func retrySendCard(url, title, content, note, template string) error {
	var err error
	for i := 0; i < 2; i++ {
		err = sendCard(url, title, content, note, template)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second * 1)
	}
	return err
}

// SendAggregatedCard aggregates messages with identical title and content
// within a specific time window to prevent spamming the Feishu API.
func sendAggregatedCard(webhookURL, title, content, note string, template CardTemplate) error {
	xl := xlog.New()
	waitDuration := time.Duration(helper.Cfg.Feishu.WaitDurationSecond) * time.Second
	xl.Debugf("waitDuration: %+v", waitDuration)

	key := fmt.Sprintf("%s:%s", title, content)

	actual, loaded := aggregationMap.LoadOrStore(key, &msgRecord{
		title:     title,
		content:   content,
		note:      note,
		template:  template,
		count:     1,
		firstSeen: time.Now(),
	})

	if loaded {
		record := actual.(*msgRecord)
		atomic.AddInt32(&record.count, 1)
		return nil
	}

	go func() {
		_ = retrySendCard(webhookURL, title, content, note, string(template))
	}()

	time.AfterFunc(waitDuration, func() {
		// LoadAndDelete ensures the record is removed from the map immediately
		// after being retrieved, preventing memory leaks.
		val, ok := aggregationMap.LoadAndDelete(key)
		if !ok {
			return
		}

		record := val.(*msgRecord)
		totalCount := atomic.LoadInt32(&record.count)

		if totalCount > 1 {
			summaryContent := fmt.Sprintf("%s\n\n**💡 Aggregation Report**\nThis message triggered **%d** times in the last %v.",
				record.content, totalCount, waitDuration)
			go func() {
				_ = retrySendCard(webhookURL, record.title, summaryContent, record.note, string(record.template))
			}()
		}
		length := 0
		aggregationMap.Range(func(key, value interface{}) bool {
			length++
			return true
		})
		xl.Debugf("aggregation map length: %v", length)
	})

	return nil
}

// SendCard sends a rich card with customizable title color based on severity
// title:    card header title
// content:  main body in Lark Markdown
// note:     optional small note (e.g. timestamp)
// template: header background color (use constants above)
func SendCard(title, content, note string, template CardTemplate) error {
	xl := xlog.New()
	if *helper.Cfg.Feishu.Enable == false {
		xl.Debugf("feishu disable: %v %v %v", title, content, note)
		return nil
	}

	webhook := helper.Cfg.Feishu.URL
	if webhook == "" {
		xl.Debugf("feishu: webhook not configured, skipping SendCard")
		return nil
	}

	// return sendCard(webhook, title, content, note, string(template))
	return sendAggregatedCard(webhook, title, content, note, template)
}

// SendError sends a red alert card (for errors, failures, security issues)
func SendError(title, content string) error {
	return SendErrorWithNote(title, content, "Time: "+time.Now().Format(time.RFC3339)+"\nSender: "+helper.Cfg.Feishu.Sender)
}

// SendErrorWithNote sends a red alert with custom note
func SendErrorWithNote(title, content, note string) error {
	return SendCard(title, content, note, TemplateRed)
}

// SendWarning sends a yellow warning card
func SendWarning(title, content string) error {
	return SendCard(title, content, "Time: "+time.Now().Format(time.RFC3339)+"\nSender: "+helper.Cfg.Feishu.Sender, TemplateYellow)
}

// SendSuccess sends a green success card
func SendSuccess(title, content string) error {
	return SendCard(title, content, "Time: "+time.Now().Format(time.RFC3339)+"\nSender: "+helper.Cfg.Feishu.Sender, TemplateGreen)
}

// SendInfo sends a blue informational card
func SendInfo(title, content string) error {
	return SendCard(title, content, "", TemplateBlue)
}

// sendCard is the core implementation
func sendCard(webhookURL, title, content, note, templateColor string) error {
	elements := []map[string]interface{}{
		{
			"tag": "div",
			"text": map[string]interface{}{
				"tag":     "lark_md",
				"content": content,
			},
		},
	}

	if note != "" {
		elements = append(elements, map[string]interface{}{
			"tag": "note",
			"elements": []map[string]interface{}{
				{
					"tag":     "plain_text",
					"content": note,
				},
			},
		})
	}

	payload := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"config": map[string]bool{
				"wide_screen_mode": true,
				"enable_forward":   true,
			},
			"header": map[string]interface{}{
				"title": map[string]string{
					"tag":     "plain_text",
					"content": title,
				},
				"template": templateColor, // dynamic color
			},
			"elements": elements,
		},
	}

	return send(webhookURL, payload)
}

// sendSimpleText sends plain text message (kept for compatibility or simple logs)
func sendSimpleText(webhookURL, text string) error {
	xl := xlog.New()
	if webhookURL == "" {
		xl.Debugf("feishu: webhook not configured, skipping SendSimpleText")
		return nil
	}

	payload := map[string]interface{}{
		"msg_type": "text",
		"content": map[string]string{
			"text": text,
		},
	}

	return send(webhookURL, payload)
}

// send is the shared HTTP sender
func send(webhookURL string, payload map[string]interface{}) error {
	xl := xlog.New()

	body, err := json.Marshal(payload)
	if err != nil {
		xl.Warnf("feishu: failed to marshal payload: %v", err)
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		xl.Warnf("feishu: failed to create request: %v", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		xl.Warnf("feishu: request failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		xl.Warnf("feishu: received non-2xx status %d: %s", resp.StatusCode, string(respBody))
		return errors.New("feishu notification failed with status " + resp.Status)
	}

	xl.Infof("feishu: notification sent successfully (template: %s)", payload["card"].(map[string]interface{})["header"].(map[string]interface{})["template"])
	return nil
}
