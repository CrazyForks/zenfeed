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

package scraper

import (
	"context"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/mock"

	"github.com/glidea/zenfeed/pkg/model"
	textconvert "github.com/glidea/zenfeed/pkg/util/text_convert"
)

// --- Interface code block ---
type ScrapeSourceRSS struct {
	URL             string
	RSSHubEndpoint  string
	RSSHubRoutePath string
}

func (c *ScrapeSourceRSS) Validate() error {
	if c.URL == "" && c.RSSHubEndpoint == "" {
		return errors.New("URL or RSSHubEndpoint can not be empty at the same time")
	}
	if c.URL == "" {
		c.URL = strings.TrimSuffix(c.RSSHubEndpoint, "/") + "/" + strings.TrimPrefix(c.RSSHubRoutePath, "/")
	}
	if c.URL != "" && !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return errors.New("URL must be a valid HTTP/HTTPS URL")
	}

	return nil
}

// --- Factory code block ---
func newRSSReader(config *ScrapeSourceRSS) (reader, error) {
	if err := config.Validate(); err != nil {
		return nil, errors.Wrapf(err, "invalid RSS config")
	}

	return &rssReader{
		config: config,
		client: &gofeedClient{
			url:  config.URL,
			base: gofeed.NewParser(),
		},
	}, nil
}

// --- Implementation code block ---

type rssReader struct {
	config *ScrapeSourceRSS
	client client
}

func (r *rssReader) Read(ctx context.Context) ([]*model.Feed, error) {
	feed, err := r.client.Get(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "fetching RSS feed")
	}
	if len(feed.Items) == 0 {
		return []*model.Feed{}, nil
	}

	now := clk.Now()
	feeds := make([]*model.Feed, 0, len(feed.Items))
	for _, fi := range feed.Items {
		item, err := r.toResultFeed(now, fi)
		if err != nil {
			return nil, errors.Wrapf(err, "converting feed item")
		}

		feeds = append(feeds, item)
	}

	return feeds, nil
}

func (r *rssReader) toResultFeed(now time.Time, feedFeed *gofeed.Item) (*model.Feed, error) {
	content := r.combineContent(feedFeed.Content, feedFeed.Description)

	// Ensure the content is markdown.
	mdContent, err := textconvert.HTMLToMarkdown([]byte(content))
	if err != nil {
		return nil, errors.Wrapf(err, "converting content to markdown")
	}

	// Create the feed item.
	feed := &model.Feed{
		Labels: model.Labels{
			{Key: model.LabelType, Value: "rss"},
			{Key: model.LabelTitle, Value: feedFeed.Title},
			{Key: model.LabelLink, Value: feedFeed.Link},
			{Key: model.LabelPubTime, Value: r.parseTime(feedFeed).Format(time.RFC3339)},
			{Key: model.LabelContent, Value: string(mdContent)},
		},
		Time: now,
	}

	return feed, nil
}

// parseTime parses the publication time from the feed item.
// If the feed item does not have a publication time, it returns the current time.
func (r *rssReader) parseTime(feedFeed *gofeed.Item) time.Time {
	if feedFeed.PublishedParsed == nil {
		return clk.Now().In(time.Local)
	}

	return feedFeed.PublishedParsed.In(time.Local)
}

// combineContent combines Content and Description fields with proper formatting.
func (r *rssReader) combineContent(content, description string) string {
	switch {
	case content == "":
		return description
	case description == "":
		return content
	default:
		return strings.Join([]string{description, content}, "\n\n")
	}
}

type client interface {
	Get(ctx context.Context) (*gofeed.Feed, error)
}

type gofeedClient struct {
	url  string
	base *gofeed.Parser
}

func (c *gofeedClient) Get(ctx context.Context) (*gofeed.Feed, error) {
	return c.base.ParseURLWithContext(c.url, ctx)
}

type mockClient struct {
	mock.Mock
}

func newMockClient() *mockClient {
	return &mockClient{}
}

func (c *mockClient) Get(ctx context.Context) (*gofeed.Feed, error) {
	args := c.Called(ctx)
	if args.Error(1) != nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*gofeed.Feed), nil
}
