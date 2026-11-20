// Package aliceplugin is an out-of-tree Yandex Alice skill channel for
// PicoClaw / Hermes, packaged as a drop-in plugin module.
//
// Unlike pkg/channels/yandexalice (in-tree), this plugin enforces a strict
// access policy by default:
//
//   - An empty allowlist means NOBODY is allowed (fail-closed), not "everyone".
//   - A debug mode can echo the caller's Yandex user_id back to them so the
//     owner can discover their own ID and put it into the allowlist.
//
// It registers itself through the public extension points only:
//
//	config.RegisterChannelSettings(ChannelType, Settings{}) // config validation
//	channels.RegisterFactory(ChannelType, ...)              // constructor
//
// Import it for its side effects only:
//
//	import _ "github.com/sipeed/picoclaw/pkg/channels/aliceplugin"
package aliceplugin
