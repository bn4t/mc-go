package mcproto

import (
    "bytes"
    "context"
    "encoding/binary"
    "crypto/rand"
    "crypto/rsa"
    "crypto/sha1"
    "crypto/x509"
    "errors"
    "math/big"
    "net"
    "time"

    "github.com/benj/mc-proto-go/codec"
    "github.com/benj/mc-proto-go/dns"
    "github.com/benj/mc-proto-go/packets"
    "github.com/benj/mc-proto-go/types"
    "github.com/benj/mc-proto-go/auth"
)

// DialOptions controls dialing and SRV behavior.
type DialOptions struct {
    Address         string        // host[:port]
    SRV             bool          // resolve _minecraft._tcp if no port
    Timeout         time.Duration // overall timeout
    ProtocolVersion int32         // for handshake; 0 lets server decide in SLP
}

// Conn is a thin wrapper over a framed connection.
type Conn struct {
    netConn net.Conn
    Framed  *codec.FramedConn
}

func (c *Conn) Close() error { return c.netConn.Close() }

// SetCompression toggles zlib compression on the connection.
func (c *Conn) SetCompression(threshold int, level int) { c.Framed.SetCompression(threshold, level) }

// EnableEncryption switches the underlying framed streams to AES-128-CFB8.
func (c *Conn) EnableEncryption(secret []byte) error { return c.Framed.EnableEncryption(secret) }

// Flush flushes buffered frames to the socket.
func (c *Conn) Flush() error { return c.Framed.Flush() }

// Dial establishes a TCP connection to a Minecraft server.
func Dial(ctx context.Context, opts DialOptions) (*Conn, string, int, error) {
    host, port := splitAddress(opts.Address)
    if opts.SRV && !hasPort(opts.Address) {
        h, p, err := dns.ResolveSRV(host)
        if err == nil {
            host, port = h, p
        }
    } else if port == 0 {
        port = 25565
    }

    d := net.Dialer{}
    if opts.Timeout > 0 {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
        defer cancel()
    }
    conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, itoa(port)))
    if err != nil {
        return nil, host, port, err
    }
    fc := codec.NewFramedConn(conn)
    return &Conn{netConn: conn, Framed: fc}, host, port, nil
}

// Status performs a one-shot server list ping.
func Status(ctx context.Context, address string, useSRV bool) (packets.StatusResponse, time.Duration, error) {
    conn, host, port, err := Dial(ctx, DialOptions{Address: address, SRV: useSRV, Timeout: 10 * time.Second})
    if err != nil {
        return packets.StatusResponse{}, 0, err
    }
    defer conn.Close()

    // 1) Handshake to switch to Status state (nextState=1)
    hs := packets.Handshake{
        ProtocolVersion: 0, // neutral for SLP; user can customize via lower-level API later
        ServerAddress:   host,
        ServerPort:      uint16(port),
        NextState:       1,
    }
    hsp, err := hs.MarshalBinary()
    if err != nil {
        return packets.StatusResponse{}, 0, err
    }
    if err := conn.Framed.WritePacket(packets.PacketIDHandshake, hsp); err != nil {
        return packets.StatusResponse{}, 0, err
    }

    // 2) Status Request
    if err := conn.Framed.WritePacket(packets.PacketIDStatusRequest, nil); err != nil {
        return packets.StatusResponse{}, 0, err
    }

    // 3) Read Status Response
    id, payload, err := conn.Framed.ReadPacket()
    if err != nil {
        return packets.StatusResponse{}, 0, err
    }
    if id != packets.PacketIDStatusResponse {
        return packets.StatusResponse{}, 0, errors.New("unexpected packet id for status response")
    }
    // Payload is a VarInt length + JSON string
    br := bytes.NewReader(payload)
    jsonStr, _, err := types.ReadString(br, br, 1<<20)
    if err != nil {
        return packets.StatusResponse{}, 0, err
    }
    info, err := packets.DecodeStatusResponse(jsonStr)
    if err != nil {
        return packets.StatusResponse{}, 0, err
    }

    // 4) Ping/Pong round trip
    now := time.Now()
    // Ping payload is int64 millis; send current time
    var pingBuf [8]byte
    binary.BigEndian.PutUint64(pingBuf[:], uint64(packets.NowMillis()))
    if err := conn.Framed.WritePacket(packets.PacketIDStatusPing, pingBuf[:]); err != nil {
        return info, 0, err
    }
    id2, pong, err := conn.Framed.ReadPacket()
    if err != nil {
        return info, 0, err
    }
    if id2 != packets.PacketIDStatusPong || len(pong) != 8 {
        return info, 0, errors.New("unexpected packet id or pong size")
    }
    rtt := time.Since(now)
    return info, rtt, nil
}

// Helpers
func splitAddress(addr string) (string, int) {
    if hasPort(addr) {
        h, p, err := net.SplitHostPort(addr)
        if err == nil {
            port, _ := net.LookupPort("tcp", p)
            return h, port
        }
    }
    return addr, 0
}

func hasPort(addr string) bool {
    _, _, err := net.SplitHostPort(addr)
    return err == nil
}

func itoa(v int) string {
    var b [6]byte
    n := len(b)
    for v >= 10 {
        n--
        q := v / 10
        b[n] = byte('0' + v - q*10)
        v = q
    }
    n--
    b[n] = byte('0' + v)
    return string(b[n:])
}

// Client is a thin stateful wrapper for login/play flows.
type Client struct{ Conn *Conn }

func NewClient(c *Conn) *Client { return &Client{Conn: c} }

// LoginOffline performs an offline-mode login with the given username and protocol number.
// The caller must supply a correct ProtocolVersion for the target server.
func (cl *Client) LoginOffline(ctx context.Context, username string, protocol int32) (packets.LoginSuccess, error) {
    // Handshake with next state = 2 (Login)
    host, port := cl.remoteHostPort()
    hs := packets.Handshake{
        ProtocolVersion: protocol,
        ServerAddress:   host,
        ServerPort:      uint16(port),
        NextState:       2,
    }
    hsp, err := hs.MarshalBinary()
    if err != nil {
        return packets.LoginSuccess{}, err
    }
    if err := cl.Conn.Framed.WritePacket(packets.PacketIDHandshake, hsp); err != nil {
        return packets.LoginSuccess{}, err
    }
    // Send Login Start
    ls := packets.LoginStart{Username: username}
    lsp, err := ls.MarshalBinary()
    if err != nil {
        return packets.LoginSuccess{}, err
    }
    if err := cl.Conn.Framed.WritePacket(packets.PacketIDLoginStart, lsp); err != nil {
        return packets.LoginSuccess{}, err
    }
    // Read until Login Success, handling Set Compression and Disconnect.
    deadlineCtx, cancel := withOptionalTimeout(ctx, 15*time.Second)
    defer cancel()
    for {
        select {
        case <-deadlineCtx.Done():
            return packets.LoginSuccess{}, deadlineCtx.Err()
        default:
        }
        id, payload, err := cl.Conn.Framed.ReadPacket()
        if err != nil {
            return packets.LoginSuccess{}, err
        }
        switch id {
        case packets.PacketIDSetCompression:
            thr, err := packets.DecodeSetCompression(payload)
            if err != nil {
                return packets.LoginSuccess{}, err
            }
            cl.Conn.SetCompression(thr, 0)
        case packets.PacketIDLoginSuccess:
            ok, err := packets.DecodeLoginSuccess(payload)
            if err != nil { return ok, err }
            // Some modern servers expect a login acknowledged packet.
            // Use static 1.21.4 mapping for now.
            _ = cl.Conn.Framed.WritePacket(0x03, nil)
            return ok, nil
        case packets.PacketIDLoginDisconnect:
            // Payload is a chat component JSON string; surface as error message
            br := bytes.NewReader(payload)
            msg, _, err := types.ReadString(br, br, 1<<20)
            if err != nil {
                return packets.LoginSuccess{}, errors.New("disconnected during login")
            }
            return packets.LoginSuccess{}, errors.New("login disconnect: " + msg)
        default:
            // Ignore unknowns during login for now (e.g., plugin request)
        }
    }
}

func (cl *Client) remoteHostPort() (string, int) {
    addr := cl.Conn.netConn.RemoteAddr().String()
    h, p, err := net.SplitHostPort(addr)
    if err != nil {
        return addr, 25565
    }
    port, _ := net.LookupPort("tcp", p)
    if port == 0 {
        port = 25565
    }
    return h, port
}

func withOptionalTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
    if d <= 0 {
        return context.WithCancel(ctx)
    }
    return context.WithTimeout(ctx, d)
}

// LoginOnline performs the RSA/AES handshake and sessionserver join.
func (cl *Client) LoginOnline(ctx context.Context, a auth.Auth, protocol int32) (packets.LoginSuccess, error) {
    // Handshake -> Login
    host, port := cl.remoteHostPort()
    hs := packets.Handshake{ProtocolVersion: protocol, ServerAddress: host, ServerPort: uint16(port), NextState: 2}
    hsp, err := hs.MarshalBinary()
    if err != nil { return packets.LoginSuccess{}, err }
    if err := cl.Conn.Framed.WritePacket(packets.PacketIDHandshake, hsp); err != nil { return packets.LoginSuccess{}, err }

    // Login Start
    ls := packets.LoginStart{Username: a.Username}
    lsp, err := ls.MarshalBinary()
    if err != nil { return packets.LoginSuccess{}, err }
    if err := cl.Conn.Framed.WritePacket(packets.PacketIDLoginStart, lsp); err != nil { return packets.LoginSuccess{}, err }

    // Loop until success; handle Encryption Request and Set Compression.
    for {
        id, payload, err := cl.Conn.Framed.ReadPacket()
        if err != nil { return packets.LoginSuccess{}, err }
        switch id {
        case packets.PacketIDEncryptionRequest:
            req, err := packets.DecodeEncryptionRequest(payload)
            if err != nil { return packets.LoginSuccess{}, err }
            pubAny, err := x509.ParsePKIXPublicKey(req.PublicKey)
            if err != nil { return packets.LoginSuccess{}, err }
            pub, ok := pubAny.(*rsa.PublicKey)
            if !ok { return packets.LoginSuccess{}, errors.New("server public key is not RSA") }
            // Generate 16-byte secret
            secret := make([]byte, 16)
            if _, err := rand.Read(secret); err != nil { return packets.LoginSuccess{}, err }
            // Encrypt secret and verify bytes
            encSecret, err := rsa.EncryptPKCS1v15(rand.Reader, pub, secret)
            if err != nil { return packets.LoginSuccess{}, err }
            encVerify, err := rsa.EncryptPKCS1v15(rand.Reader, pub, req.VerifyData)
            if err != nil { return packets.LoginSuccess{}, err }
            // Compute server hash
            hash := computeServerHash(req.ServerID, secret, req.PublicKey)
            // Notify session server
            if err := auth.JoinServer(ctx, nil, "", a, hash); err != nil { return packets.LoginSuccess{}, err }
            // Send Encryption Response
            er := packets.EncryptionResponse{Secret: encSecret, VerifyData: encVerify}
            erp, err := er.MarshalBinary()
            if err != nil { return packets.LoginSuccess{}, err }
            if err := cl.Conn.Framed.WritePacket(packets.PacketIDEncryptionResponse, erp); err != nil { return packets.LoginSuccess{}, err }
            // Switch to encrypted streams
            if err := cl.Conn.EnableEncryption(secret); err != nil { return packets.LoginSuccess{}, err }
        case packets.PacketIDSetCompression:
            thr, err := packets.DecodeSetCompression(payload)
            if err != nil { return packets.LoginSuccess{}, err }
            cl.Conn.SetCompression(thr, 0)
        case packets.PacketIDLoginSuccess:
            ok, err := packets.DecodeLoginSuccess(payload)
            if err != nil { return ok, err }
            _ = cl.Conn.Framed.WritePacket(0x03, nil) // Login Acknowledged for 1.21.x
            return ok, nil
        case packets.PacketIDLoginDisconnect:
            br := bytes.NewReader(payload)
            msg, _, err := types.ReadString(br, br, 1<<20)
            if err != nil { return packets.LoginSuccess{}, errors.New("login disconnect") }
            return packets.LoginSuccess{}, errors.New("login disconnect: "+msg)
        default:
            // ignore
        }
    }
}

// computeServerHash produces the Java-style signed hex of SHA1(serverId + secret + pubKey)
func computeServerHash(serverID string, secret, pubKey []byte) string {
    h := sha1.New()
    h.Write([]byte(serverID))
    h.Write(secret)
    h.Write(pubKey)
    digest := h.Sum(nil)
    // Convert to signed two's complement number in hex
    // If highest bit set, subtract 2^160 to get negative value
    i := new(big.Int).SetBytes(digest)
    if digest[0]&0x80 != 0 {
        two160 := new(big.Int).Lsh(big.NewInt(1), 160)
        i.Sub(i, two160)
    }
    return i.Text(16)
}

// Raw helpers
func (c *Conn) ReadRawPacket() (int32, []byte, error) { return c.Framed.ReadPacket() }
func (c *Conn) SendRawPacket(id int32, payload []byte) error { return c.Framed.WritePacket(id, payload) }
