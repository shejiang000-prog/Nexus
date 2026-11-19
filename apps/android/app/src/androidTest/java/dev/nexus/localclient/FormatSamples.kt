package dev.nexus.localclient

import android.util.Base64

/** Synthetic subscription nodes. Addresses are documentation ranges or .test names; no secret is real. */
object FormatSamples {
    private const val ID = "2f0d6c0e-6d4b-4f43-9c39-8f1e6a3c1d11"
    private const val KEY = "S3eQqL-hHNPrWVR81GK0BpMPJon4Fbp9HvyOq5sBe0M"
    private const val PIN = "5b2e1f0a9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f"

    val clash = """
        proxies:
          - name: reality
            type: vless
            server: 203.0.113.10
            port: 443
            uuid: $ID
            network: tcp
            tls: true
            udp: true
            flow: xtls-rprx-vision
            servername: www.qa-reality.test
            reality-opts:
              public-key: $KEY
              short-id: 0123
            client-fingerprint: chrome
          - name: vless-ws-cdn
            type: vless
            server: 203.0.113.11
            port: 443
            uuid: $ID
            tls: true
            network: ws
            ws-opts:
              path: /qa-ws
              max-early-data: 2048
              early-data-header-name: Sec-WebSocket-Protocol
              headers:
                Host: cdn.qa.test
          - name: vmess-grpc
            type: vmess
            server: vm.qa.test
            port: 443
            uuid: $ID
            alterId: 0
            cipher: auto
            tls: true
            servername: grpc.qa.test
            network: grpc
            grpc-opts:
              grpc-service-name: qa-grpc
          - name: vmess-http
            type: vmess
            server: 203.0.113.12
            port: 80
            uuid: $ID
            alterId: 0
            cipher: none
            network: http
            http-opts:
              method: GET
              path: [/qa-http]
              headers:
                Host: [http.qa.test]
          - name: trojan-upgrade
            type: trojan
            server: tr.qa.test
            port: 443
            password: 1_000
            sni: up.qa.test
            network: ws
            fingerprint: $PIN
            ws-opts:
              path: /qa-up
              v2ray-http-upgrade: true
          - name: hy2-hop
            type: hysteria2
            server: hy.qa.test
            port: 443
            ports: 20000-20010
            hop-interval: 30
            password: qa-pass
            obfs: salamander
            obfs-password: qa-obfs-secret
            sni: hy.qa.test
          - name: ss2022
            type: ss
            server: 203.0.113.13
            port: 8388
            cipher: 2022-blake3-aes-128-gcm
            password: AAECAwQFBgcICQoLDA0ODw==
          - name: xhttp
            type: vless
            server: xh.qa.test
            port: 443
            uuid: $ID
            tls: true
            network: xhttp
            servername: xh.qa.test
            xhttp-opts:
              path: /qa-xhttp
              mode: packet-up
          - name: tuic-clash
            type: tuic
            server: 203.0.113.14
            port: 443
            uuid: $ID
            password: qa-tuic
            alpn: [h3]
            sni: tuic.qa.test
            congestion-controller: bbr
            udp-relay-mode: native
            disable-sni: false
          - name: anytls
            type: anytls
            server: 203.0.113.15
            port: 443
            password: qa-anytls
            sni: any.qa.test
            client-fingerprint: chrome
          - name: insecure
            type: trojan
            server: 203.0.113.16
            port: 443
            password: qa
            skip-cert-verify: true
          - name: plugin
            type: ss
            server: 203.0.113.17
            port: 8388
            cipher: aes-128-gcm
            password: qa
            plugin: obfs
            plugin-opts:
              mode: http
          - name: h2
            type: vmess
            server: 203.0.113.18
            port: 443
            uuid: $ID
            alterId: 0
            cipher: auto
            tls: true
            network: h2
          - name: alter
            type: vmess
            server: 203.0.113.19
            port: 443
            uuid: $ID
            alterId: 64
            cipher: auto
          - name: oldcipher
            type: ss
            server: 203.0.113.20
            port: 8388
            cipher: aes-256-cfb
            password: qa
    """.trimIndent()

    val singBox = """
        {"outbounds":[
          {"type":"vless","tag":"sb-reality","server":"203.0.113.30","server_port":443,"uuid":"$ID","flow":"xtls-rprx-vision",
           "tls":{"enabled":true,"server_name":"www.sb.test","utls":{"enabled":true,"fingerprint":"firefox"},
                  "reality":{"enabled":true,"public_key":"$KEY","short_id":"a1b2"}}},
          {"type":"vmess","tag":"sb-ws","server":"sb.qa.test","server_port":443,"uuid":"$ID","security":"auto","alter_id":0,
           "tls":{"enabled":true,"server_name":"sni.sb.test"},
           "transport":{"type":"ws","path":"/sb-ws","headers":{"Host":"host.sb.test"},"max_early_data":2048,
                        "early_data_header_name":"Sec-WebSocket-Protocol"}},
          {"type":"shadowsocks","tag":"sb-ss","server":"203.0.113.31","server_port":8388,"method":"aes-256-gcm","password":"qa"},
          {"type":"hysteria2","tag":"sb-hy2","server":"203.0.113.32","server_port":443,"server_ports":["30000:30010"],
           "hop_interval":"30s","password":"qa","obfs":{"type":"salamander","password":"sb-obfs"},
           "tls":{"enabled":true,"server_name":"hy.sb.test","alpn":["h3"]}},
          {"type":"trojan","tag":"sb-http","server":"203.0.113.33","server_port":443,"password":"qa",
           "tls":{"enabled":true},"transport":{"type":"http"}},
          {"type":"selector","tag":"proxy","outbounds":["sb-reality"]},
          {"type":"direct","tag":"direct"}
        ]}
    """.trimIndent()

    private val vmessJson = Base64.encodeToString(("""{"v":"2","ps":"link-vmess-json","add":"198.51.100.7","port":"443",""" +
        """"id":"$ID","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.vm.test","path":"/vm-ws",""" +
        """"tls":"tls","sni":"","alpn":"","fp":""}""").toByteArray(), Base64.NO_WRAP)

    val links = listOf(
        "ssr://dW5zdXBwb3J0ZWQ",
        "vless://$ID@203.0.113.40:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.link.test&fp=safari&pbk=$KEY&sid=ab12&type=tcp#link-reality",
        "vless://$ID@203.0.113.41:443?encryption=none&security=tls&type=ws&host=lws.qa.test&path=%2Flws#link-ws-host",
        "vmess://$vmessJson",
        "tuic://$ID:qa-pass@203.0.113.42:443?sni=tuic.link.test&alpn=h3&congestion_control=bbr&udp_relay_mode=native#link-tuic",
        "hysteria2://qa-pass@203.0.113.43:443?sni=hy.link.test&insecure=1#link-hy2-insecure",
    ).joinToString("\n")

    /** Name to expected tokens in the converted outbound, or "!" and a fragment of the refusal reason. */
    val cases = linkedMapOf(
        "reality" to "\"reality\"|$KEY|\"0123\"|www.qa-reality.test|xtls-rprx-vision|chrome",
        "vless-ws-cdn" to "/qa-ws?ed=2048|\"serverName\":\"cdn.qa.test\"",
        "vmess-grpc" to "qa-grpc|\"serverName\":\"grpc.qa.test\"",
        "vmess-http" to "/qa-http|http.qa.test",
        "trojan-upgrade" to "httpupgrade|/qa-up|\"serverName\":\"up.qa.test\"|1_000|$PIN",
        "hy2-hop" to "hysteria|salamander|qa-obfs-secret|20010",
        "ss2022" to "2022-blake3-aes-128-gcm",
        "xhttp" to "/qa-xhttp|packet-up",
        "tuic-clash" to "shoesClient|tuic.qa.test|h3",
        "anytls" to "anytls|any.qa.test",
        "insecure" to "!跳过证书验证",
        "plugin" to "!插件",
        "h2" to "!HTTP/2",
        "alter" to "!alterId",
        "oldcipher" to "!加密方式",
        "sb-reality" to "\"reality\"|$KEY|a1b2|www.sb.test|firefox",
        "sb-ws" to "/sb-ws?ed=2048|host.sb.test|\"serverName\":\"sni.sb.test\"",
        "sb-ss" to "aes-256-gcm",
        "sb-hy2" to "salamander|sb-obfs|30010|\"serverName\":\"hy.sb.test\"",
        "sb-http" to "!HTTP/2",
        "link-reality" to "\"reality\"|$KEY|ab12|www.link.test|safari",
        "link-ws-host" to "\"serverName\":\"lws.qa.test\"",
        "link-vmess-json" to "\"serverName\":\"cdn.vm.test\"|/vm-ws",
        "link-tuic" to "shoesClient|tuic.link.test",
        "link-hy2-insecure" to "!跳过证书验证",
    )
}
