// Copyright (C) 2025 wangyusong
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package channel

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/gomail.v2"

	"github.com/glidea/zenfeed/pkg/model"
	"github.com/glidea/zenfeed/pkg/notify/route"
	"github.com/glidea/zenfeed/pkg/util/buffer"
	textconvert "github.com/glidea/zenfeed/pkg/util/text_convert"
	timeutil "github.com/glidea/zenfeed/pkg/util/time"
)

type Email struct {
	SmtpEndpoint string
	host         string
	port         int
	From         string
	Password     string

	FeedMarkdownTemplate string
	feedMakrdownTemplate *template.Template

	FeedHTMLSnippetTemplate string
	feedHTMLSnippetTemplate *template.Template
}

func (c *Email) Validate() error {
	if c.SmtpEndpoint == "" {
		return errors.New("email.smtp_endpoint is required")
	}
	parts := strings.Split(c.SmtpEndpoint, ":")
	if len(parts) != 2 {
		return errors.New("email.smtp_endpoint must be in the format host:port")
	}
	c.host = parts[0]
	var err error
	c.port, err = strconv.Atoi(parts[1])
	if err != nil {
		return errors.Wrap(err, "invalid email.smtp_endpoint")
	}
	if c.From == "" {
		return errors.New("email.from is required")
	}
	if c.FeedMarkdownTemplate == "" {
		c.FeedMarkdownTemplate = fmt.Sprintf("{{.%s}}", model.LabelContent)
	}
	t, err := template.New("").Parse(c.FeedMarkdownTemplate)
	if err != nil {
		return errors.Wrap(err, "parse feed markdown template")
	}
	c.feedMakrdownTemplate = t
	if c.FeedHTMLSnippetTemplate != "" {
		t, err := template.New("").Parse(c.FeedHTMLSnippetTemplate)
		if err != nil {
			return errors.Wrap(err, "parse feed html snippet template")
		}
		c.feedHTMLSnippetTemplate = t
	}

	return nil
}

func newEmail(c *Email, dependencies Dependencies) (sender, error) {
	host, portStr, err := net.SplitHostPort(c.SmtpEndpoint)
	if err != nil {
		return nil, errors.Wrap(err, "split host port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, errors.Wrap(err, "convert port to int")
	}

	return &email{
		config:       c,
		dependencies: dependencies,
		dialer:       gomail.NewDialer(host, port, c.From, c.Password),
	}, nil
}

type email struct {
	config       *Email
	dependencies Dependencies
	dialer       *gomail.Dialer
}

func (e *email) Send(ctx context.Context, receiver Receiver, group *route.FeedGroup) error {
	email, err := e.buildEmail(receiver, group)
	if err != nil {
		return errors.Wrap(err, "build email")
	}

	if err := e.dialer.DialAndSend(email); err != nil {
		return errors.Wrap(err, "send email")
	}

	return nil
}

func (e *email) buildEmail(receiver Receiver, group *route.FeedGroup) (*gomail.Message, error) {
	m := gomail.NewMessage()
	m.SetHeader("From", e.config.From)
	m.SetHeader("To", receiver.Email)
	m.SetHeader("Subject", group.Name)

	body, err := e.buildBodyHTML(group.Feeds)
	if err != nil {
		return nil, errors.Wrap(err, "build email body HTML")
	}
	m.SetBody("text/html", string(body))

	return m, nil
}

func (e *email) buildBodyHTML(feeds []*route.Feed) ([]byte, error) {
	bodyBuf := buffer.Get()
	defer buffer.Put(bodyBuf)

	// Write HTML header.
	if err := e.writeHTMLHeader(bodyBuf); err != nil {
		return nil, errors.Wrap(err, "write HTML header")
	}

	// Write each feed content.
	for i, feed := range feeds {
		if err := e.writeFeedContent(bodyBuf, feed); err != nil {
			return nil, errors.Wrap(err, "write feed content")
		}

		// Add separator (except the last feed).
		if i < len(feeds)-1 {
			if err := e.writeSeparator(bodyBuf); err != nil {
				return nil, errors.Wrap(err, "write separator")
			}
		}
	}

	// Write disclaimer and HTML footer.
	if err := e.writeDisclaimer(bodyBuf); err != nil {
		return nil, errors.Wrap(err, "write disclaimer")
	}
	if err := e.writeHTMLFooter(bodyBuf); err != nil {
		return nil, errors.Wrap(err, "write HTML footer")
	}

	return bodyBuf.Bytes(), nil
}

func (e *email) writeHTMLHeader(buf *buffer.Bytes) error {
	_, err := buf.WriteString(`<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Summary</title>
</head>
<body style="margin:0; padding:0; background-color:#f5f7fa; font-family:'Google Sans',Roboto,Arial,sans-serif;">
  <div style="max-width:650px; margin:0 auto; padding:30px 20px;">
    <div style="background-color:#ffffff; border-radius:12px; box-shadow:0 5px 15px rgba(0,0,0,0.08); padding:30px; margin-bottom:30px;">`)

	return err
}

const timeLayout = "01-02 15:04"

func (e *email) writeFeedContent(buf *buffer.Bytes, feed *route.Feed) error {
	// Write title and source information.
	if err := e.writeFeedHeader(buf, feed); err != nil {
		return errors.Wrap(err, "write feed header")
	}

	// Write content.
	if err := e.writeFeedBody(buf, feed); err != nil {
		return errors.Wrap(err, "write feed body")
	}

	// Write related articles.
	if len(feed.Related) > 0 {
		if err := e.writeRelateds(buf, feed.Related); err != nil {
			return errors.Wrap(err, "write relateds")
		}
	}

	if _, err := buf.WriteString(`
      </div>`); err != nil {
		return errors.Wrap(err, "write feed footer")
	}

	return nil
}

func (e *email) writeFeedHeader(buf *buffer.Bytes, feed *route.Feed) error {
	typ := feed.Labels.Get(model.LabelType)
	source := feed.Labels.Get(model.LabelSource)
	title := feed.Labels.Get(model.LabelTitle)
	link := feed.Labels.Get(model.LabelLink)
	pubTimeI, _ := timeutil.Parse(feed.Labels.Get(model.LabelPubTime))
	pubTime := pubTimeI.In(time.Local).Format(timeLayout)
	scrapeTime := feed.Time.In(time.Local).Format(timeLayout)

	if _, err := fmt.Fprintf(buf, `
      <div style="margin-bottom:30px;">
        <h2 style="font-size:22px; font-weight:500; color:#202124; margin:0 0 10px 0;">
          %s
        </h2>
        <p style="font-size:14px; color:#5f6368; margin:0 0 15px 0;">Source: <a href="%s" style="color:#1a73e8; text-decoration:none;">%s/%s</a></p>
        <p style="font-size:14px; color:#5f6368; margin:0 0 15px 0;">Published: %s | Scraped: %s</p>`,
		title, link, typ, source, pubTime, scrapeTime); err != nil {
		return errors.Wrap(err, "write feed header")
	}

	return nil
}

func (e *email) writeFeedBody(buf *buffer.Bytes, feed *route.Feed) error {
	if _, err := buf.WriteString(`<div style="font-size:15px; color:#444; line-height:1.7;">
		<style>
			img {
				max-width: 100%;
				height: auto;
				display: block;
				margin: 10px 0;
			}
			pre, code {
				white-space: pre-wrap;
				word-wrap: break-word;
				overflow-wrap: break-word;
				max-width: 100%;
				overflow-x: auto;
			}
			table {
				max-width: 100%;
				overflow-x: auto;
				display: block;
			}
		</style>`); err != nil {
		return errors.Wrap(err, "write feed body header")
	}

	if _, err := e.renderFeedContent(buf, feed); err != nil {
		return errors.Wrap(err, "render feed content")
	}

	if _, err := buf.WriteString(`</div>`); err != nil {
		return errors.Wrap(err, "write feed body footer")
	}

	return nil
}

func (e *email) renderFeedContent(buf *buffer.Bytes, feed *route.Feed) (n int, err error) {
	if e.config.feedHTMLSnippetTemplate != nil {
		n, err = e.renderHTMLContent(buf, feed)
		if err == nil && n > 0 {
			return
		}
	}

	// Fallback to markdown.
	return e.renderMarkdownContent(buf, feed)
}

func (e *email) renderHTMLContent(buf *buffer.Bytes, feed *route.Feed) (n int, err error) {
	oldN := buf.Len()
	if err := e.config.feedHTMLSnippetTemplate.Execute(buf, feed.Labels.Map()); err != nil {
		return 0, errors.Wrap(err, "execute feed HTML template")
	}

	return buf.Len() - oldN, nil
}

func (e *email) renderMarkdownContent(buf *buffer.Bytes, feed *route.Feed) (n int, err error) {
	oldN := buf.Len()
	tempBuf := buffer.Get()
	defer buffer.Put(tempBuf)

	if err := e.config.feedMakrdownTemplate.Execute(tempBuf, feed.Labels.Map()); err != nil {
		return 0, errors.Wrap(err, "execute feed markdown template")
	}

	contentMarkdown := tempBuf.Bytes()
	contentHTML, err := textconvert.MarkdownToHTML(contentMarkdown)
	if err != nil {
		return 0, errors.Wrap(err, "markdown to HTML")
	}

	if _, err := buf.Write(contentHTML); err != nil {
		return 0, errors.Wrap(err, "write content HTML")
	}

	return buf.Len() - oldN, nil
}

func (e *email) writeRelateds(buf *buffer.Bytes, related []*route.Feed) error {
	if _, err := buf.WriteString(`
        <div style="margin-top:20px; padding-top:15px; border-top:1px solid #f1f3f4;">
          <p style="font-size:16px; font-weight:500; color:#1a73e8; margin:0 0 10px 0;">Related:</p>`); err != nil {
		return errors.Wrapf(err, "write relateds header")
	}

	for _, f := range related {
		relTyp := f.Labels.Get(model.LabelType)
		relSource := f.Labels.Get(model.LabelSource)
		relTitle := f.Labels.Get(model.LabelTitle)
		relLink := f.Labels.Get(model.LabelLink)

		if _, err := fmt.Fprintf(buf, `
          <div style="margin-bottom:8px; padding-left:15px; position:relative;">
            <span style="position:absolute; left:0; top:8px; width:6px; height:6px; background-color:#4285f4; border-radius:50%%;"></span>
            <a href="%s" style="color:#1a73e8; text-decoration:none;">%s/%s: %s</a>
          </div>`, relLink, relTyp, relSource, relTitle); err != nil {
			return errors.Wrapf(err, "write relateds item")
		}
	}

	if _, err := buf.WriteString(`
        </div>`); err != nil {
		return errors.Wrapf(err, "write relateds footer")
	}

	return nil
}

func (e *email) writeSeparator(buf *buffer.Bytes) error {
	_, err := buf.WriteString(`
      <hr style="border:0; height:1px; background:linear-gradient(to right, rgba(0,0,0,0.03), rgba(0,0,0,0.1), rgba(0,0,0,0.03)); margin:25px 0;">`)

	return err
}

func (e *email) writeDisclaimer(buf *buffer.Bytes) error {
	_, err := buf.WriteString(`
      <div style="margin-top:40px; padding:25px; border-top:2px solid #e0e0e0; font-family:'Google Sans',Roboto,Arial,sans-serif; font-size:14px; line-height:1.8; color:#4a4a4a; text-align:center; background-color:#f8f9fa; border-radius:8px;">
        <p style="margin:0 0 15px 0;">
          <strong style="color:#1a73e8; font-size:15px;">免责声明 / Disclaimer</strong><br>
          <span style="display:block; margin-top:8px;">本邮件内容仅用于个人概括性学习和理解，版权归原作者所有。</span>
          <span style="display:block; color:#666;">This email content is for personal learning and understanding purposes only. All rights reserved to the original author.</span>
        </p>
        <p style="margin:0 0 15px 0;">
          <strong style="color:#ea4335; font-size:15px;">严禁二次分发或传播！！！<br>NO redistribution or sharing!!!</strong>
        </p>
        <p style="margin:0; font-size:13px; color:#666;">
          如有侵权，请联系 / For copyright issues, please contact:<br>
          <a href="mailto:ysking7402@gmail.com" style="color:#1a73e8; text-decoration:none;">ysking7402@gmail.com</a>
        </p>
      </div>`)

	return err
}

func (e *email) writeHTMLFooter(buf *buffer.Bytes) error {
	_, err := buf.WriteString(`
    </div>
  </div>
</body>
</html>`)

	return err
}
