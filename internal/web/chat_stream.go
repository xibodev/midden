package web

import (
	"context"
	"encoding/json"
	"time"

	"github.com/xibodev/facet-studio/pkg/bus"
	"github.com/xibodev/facet-studio/pkg/config"
	"github.com/xibodev/facet-studio/pkg/events"
)

// A channel adapter for the kernel's supported accumulated-text streamer.
// It forwards final-answer content only; reasoning is intentionally not exposed.
type chatStreamDelegate struct{ publish func(events.Event) }
type chatStreamer struct {
	key     string
	publish func(events.Event)
}

func (d chatStreamDelegate) GetStreamer(_ context.Context, channel, _, key string) (bus.Streamer, bool) {
	if channel != "cli" {
		return nil, false
	}
	return &chatStreamer{key: key, publish: d.publish}, true
}
func (s *chatStreamer) send(kind events.Kind, text string) {
	s.publish(events.Event{Kind: kind, Time: time.Now(), Source: events.Source{Component: "midden", Name: "browser-stream"}, Scope: events.Scope{SessionKey: s.key}, Payload: map[string]any{"content": text}})
}
func (s *chatStreamer) Update(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.send("midden.chat.content", text)
	return nil
}
func (s *chatStreamer) Finalize(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.send("midden.chat.final", text)
	return nil
}
func (s *chatStreamer) Cancel(context.Context) { s.send("midden.chat.cancelled", "") }

func configureChatStreaming(cfg *config.Config) error {
	if cfg.Channels == nil {
		cfg.Channels = config.ChannelsConfig{}
	}
	settings := config.PicoSettings{Streaming: config.StreamingConfig{Enabled: true}}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	cfg.Channels["cli"] = &config.Channel{Type: config.ChannelPico, Enabled: true, Settings: config.RawNode(raw)}
	return config.InitChannelList(cfg.Channels)
}
