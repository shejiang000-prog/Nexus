package dev.nexus.vpnprobe;

import android.app.Activity;
import android.app.Instrumentation;
import android.content.Context;
import android.content.Intent;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.os.Bundle;
import android.os.SystemClock;
import java.io.InputStream;
import java.net.ConnectException;
import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.URL;
import java.security.SecureRandom;
import java.util.Arrays;
import javax.net.ssl.HttpsURLConnection;

/** Deliberately uses ordinary, unbound app sockets under a separate Android UID. */
public final class Probe extends Instrumentation {
    private Bundle options;
    @Override public void onCreate(Bundle args) {
        super.onCreate(args);
        options = args;
        start();
    }
    @Override public void onStart() {
        Bundle result = new Bundle();
        Activity foreground = null;
        int status = Activity.RESULT_CANCELED;
        try {
            Intent visible = new Intent().setClassName(getTargetContext(), Activity.class.getName())
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            foreground = startActivitySync(visible);
            ConnectivityManager cm = (ConnectivityManager)getContext().getSystemService(Context.CONNECTIVITY_SERVICE);
            Network network;
            NetworkCapabilities capabilities;
            long deadline = SystemClock.elapsedRealtime() + 10000;
            do {
                network = cm.getActiveNetwork();
                capabilities = cm.getNetworkCapabilities(network);
                if (capabilities != null) break;
                SystemClock.sleep(100);
            } while (SystemClock.elapsedRealtime() < deadline);
            if (capabilities == null) throw new AssertionError("foreground app has no default network");
            boolean vpn = capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN);
            if (vpn != "on".equals(options.getString("vpn"))) throw new AssertionError("unexpected default network");
            result.putString("default", vpn ? "VPN" : "ordinary network");
            if (options.containsKey("m3")) {
                if (!vpn || !"192.0.2.85".equals(options.getString("echo"))) throw new AssertionError("requires dedicated M3 VPN target");
                InetSocketAddress target = new InetSocketAddress(options.getString("echo"), Integer.parseInt(options.getString("port")));
                try (Socket sock = new Socket()) {
                    sock.connect(target, 5000); sock.setSoTimeout(5000);
                    sock.getOutputStream().write(65);
                    if (sock.getInputStream().read()!=65) throw new AssertionError("TCP receipt");
                }
                try (DatagramSocket sock = new DatagramSocket()) {
                    byte[] nonce = new SecureRandom().generateSeed(32);
                    sock.connect(target); sock.setSoTimeout(5000); sock.send(new DatagramPacket(nonce, nonce.length));
                    DatagramPacket reply = new DatagramPacket(new byte[64],64); sock.receive(reply);
                    if (!Arrays.equals(nonce, Arrays.copyOf(reply.getData(),reply.getLength()))) throw new AssertionError("UDP receipt");
                }
                result.putString("m3", "separate UID TCP and UDP verified");
                status = Activity.RESULT_OK;
                return;
            }
            String resolver = options.getString("dns");
            if (vpn && !cm.getLinkProperties(network).getDnsServers().contains(InetAddress.getByName(resolver)))
                throw new AssertionError("incorrect VPN DNS");
            for (String host : new String[]{"example.com", "example.org"}) {
                result.putString("stage", host + " DNS");
                if (InetAddress.getAllByName(host).length == 0) throw new AssertionError("empty DNS answer");
                result.putString("stage", host + " HTTPS");
                HttpsURLConnection c = (HttpsURLConnection)new URL("https://" + host + "/?nexus=" + System.nanoTime()).openConnection();
                try {
                    c.setUseCaches(false);
                    c.setConnectTimeout(10000);
                    c.setReadTimeout(10000);
                    if (c.getResponseCode() != 200) throw new AssertionError("HTTP " + c.getResponseCode());
                    try (InputStream in = c.getInputStream()) {
                        byte[] body = new byte[4096];
                        int count = 0, n;
                        while (count < body.length && (n = in.read(body, count, body.length-count)) > 0) count += n;
                        if (!new String(body, 0, count, java.nio.charset.StandardCharsets.UTF_8).contains("Example Domain"))
                            throw new AssertionError("unexpected page");
                    }
                } finally { c.disconnect(); }
                result.putString(host, "HTTPS 200, verified page");
            }
            if (vpn && !"only".equals(options.getString("https"))) {
                // The lab node denies documentation addresses. A premature local SYN-ACK
                // would let connect() succeed and break a browser's IPv4 fallback.
                for (String address : options.containsKey("subscription") ? new String[0] : new String[]{"192.0.2.123", "2001:db8::123"}) {
                    // A direct route can black-hole a SYN. Wait for the native 8 s setup deadline and its RST.
                    try (Socket denied = new Socket()) {
                        denied.connect(new InetSocketAddress(InetAddress.getByName(address), 443), 12000);
                        throw new AssertionError("unreachable destination completed TCP handshake: " + address);
                    } catch (ConnectException expected) {
                        result.putString(address, "TCP connection refused before handshake");
                    }
                }
                InetAddress peer = InetAddress.getByName(options.getString("echo"));
                int port = Integer.parseInt(options.getString("port"));
                try (DatagramSocket udp = new DatagramSocket()) {
                    udp.connect(peer, port);
                    udp.setSoTimeout(6000);
                    for (int size : new int[]{32, 1200, 1472}) {
                        result.putString("stage", "UDP " + size);
                        byte[] payload = new byte[size];
                        new SecureRandom().nextBytes(payload);
                        udp.send(new DatagramPacket(payload, payload.length));
                        DatagramPacket reply = new DatagramPacket(new byte[1500], 1500);
                        udp.receive(reply);
                        if (!Arrays.equals(payload, Arrays.copyOf(reply.getData(), reply.getLength())))
                            throw new AssertionError("UDP mismatch");
                    }
                }
                result.putString("udp", "32,1200,1472 byte echoes");
            }
            status = Activity.RESULT_OK;
        } catch (Exception | AssertionError failure) {
            result.putString("error", failure.toString());
        } finally {
            if (foreground != null) runOnMainSync(foreground::finish);
            finish(status, result);
        }
    }
}
