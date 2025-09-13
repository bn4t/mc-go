package main

import (
    "encoding/hex"
    "fmt"
    "github.com/benj/mc-proto-go/proto"
)

// Minimal demo: build and then decode a Login Disconnect packet using the generated 1.21.8 schema.
func main() {
    // Build a disconnect payload with reason "Bye"
    payload, err := proto.PC_1_21_8.EncodePacket("login", "toClient", "packet_disconnect", map[string]any{
        "reason": "Bye",
    })
    if err != nil { panic(err) }
    fmt.Println("payload:", hex.EncodeToString(payload))

    // In a real stream this sits after the VarInt packet id and length; here we decode just the params.
    name, v, err := proto.PC_1_21_8.DecodePacket("login", "toClient", proto.PC_1_21_8.States["login"].ToClient.NameToID["packet_disconnect"], payload)
    if err != nil { panic(err) }
    fmt.Println("decoded name:", name)
    fmt.Println("decoded value:", v)
}

