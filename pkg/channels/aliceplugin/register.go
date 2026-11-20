package aliceplugin

import (
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

// ChannelType is the config `type` value this plugin answers to.
//
// It intentionally differs from the in-tree "yandex_alice" so both can coexist
// and so a config typo cannot silently fall back to the permissive built-in.
const ChannelType = "yandex_alice_secure"

func init() {
	config.RegisterChannelSettings(ChannelType, Settings{})

	channels.RegisterFactory(
		ChannelType,
		func(channelName, channelType string, cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			bc := cfg.Channels[channelName]
			if bc == nil {
				return nil, channels.ErrSendFailed
			}
			decoded, err := bc.GetDecoded()
			if err != nil {
				return nil, err
			}
			s, ok := decoded.(*Settings)
			if !ok {
				return nil, channels.ErrSendFailed
			}
			ch, err := New(channelName, bc, s, b)
			if err != nil {
				return nil, err
			}
			return ch, nil
		},
	)
}
