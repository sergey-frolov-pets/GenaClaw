package main

import (
	"fmt"

	_ "github.com/sipeed/picoclaw/pkg/gateway"
	"github.com/sipeed/picoclaw/pkg/channels"
)

func main() {
	names := channels.GetRegisteredFactoryNames()
	fmt.Println("registered factories:", names)
	_, ok := channels.LookupFactoryForTest("vk")
	fmt.Println("vk registered:", ok)
}
