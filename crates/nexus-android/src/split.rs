use std::{
    collections::{HashMap, HashSet},
    net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr},
    sync::Mutex,
    time::{Duration, Instant},
};

pub struct Rules {
    suffix: HashSet<String>,
    exact: HashSet<String>,
    patterns: Vec<fn(&str) -> bool>,
    ranges: [Vec<(u128, u128)>; 2],
    learned: Mutex<HashMap<IpAddr, Instant>>,
}

fn aws(name: &str, cn: bool) -> bool {
    let Some((prefix, tld)) = name.rsplit_once('.') else {
        return false;
    };
    let Some((parent, code)) = prefix.rsplit_once(".awsdns-cn-") else {
        return false;
    };
    let b = code.as_bytes();
    !parent.is_empty()
        && b.len() == 2
        && b[0].is_ascii_digit()
        && if cn {
            tld == "cn" && (b[1].is_ascii_digit() || (b'a'..=b'e').contains(&b[1]))
        } else {
            matches!(tld, "biz" | "com" | "net" | "top") && b[1].is_ascii_digit()
        }
}

fn number(ip: IpAddr) -> (usize, u128, u32) {
    match ip {
        IpAddr::V4(a) => (0, u32::from(a).into(), 32),
        IpAddr::V6(a) => (1, u128::from(a), 128),
    }
}

impl Rules {
    pub fn parse(text: &str) -> Option<Self> {
        let mut r = Self {
            suffix: HashSet::new(),
            exact: HashSet::new(),
            patterns: Vec::new(),
            ranges: Default::default(),
            learned: Mutex::default(),
        };
        for line in text.lines() {
            let (kind, value) = line.split_once(':')?;
            match kind {
                "domain" => {
                    r.suffix.insert(value.into());
                }
                "full" => {
                    r.exact.insert(value.into());
                }
                "regexp" => r.patterns.push(match value {
                    r".+\.awsdns-cn-[0-9][0-9]\.(biz|com|net|top)$" => |s| aws(s, false),
                    r".+\.awsdns-cn-[0-9][a-e0-9]\.cn$" => |s| aws(s, true),
                    r"^.+-mihayo\.akamaized\.net$" => |s| {
                        s.strip_suffix("-mihayo.akamaized.net")
                            .is_some_and(|p| !p.is_empty())
                    },
                    _ => return None,
                }),
                "ip" => {
                    let (ip, prefix) = value.split_once('/')?;
                    let (family, start, bits) = number(ip.parse().ok()?);
                    let host = bits.checked_sub(prefix.parse().ok()?)?;
                    let mask = 1u128.checked_shl(host).map_or(u128::MAX, |n| n - 1);
                    if start & mask != 0 {
                        return None;
                    }
                    r.ranges[family].push((start, start | mask));
                }
                _ => return None,
            }
        }
        for ranges in &mut r.ranges {
            ranges.sort_unstable();
            if ranges.windows(2).any(|v| v[0].1 >= v[1].0) {
                return None;
            }
        }
        Some(r)
    }
    pub fn domain(&self, name: &str) -> bool {
        if self.exact.contains(name) || self.patterns.iter().any(|matches| matches(name)) {
            return true;
        }
        let mut suffix = name;
        loop {
            if self.suffix.contains(suffix) {
                return true;
            }
            let Some((_, next)) = suffix.split_once('.') else {
                return false;
            };
            suffix = next;
        }
    }
    pub fn learned(&self, ip: IpAddr, now: Instant) -> bool {
        let mut cache = self.learned.lock().unwrap();
        if cache.get(&ip).is_some_and(|end| *end > now) {
            return true;
        }
        cache.remove(&ip);
        false
    }
    pub fn direct(&self, ip: IpAddr, now: Instant) -> bool {
        let (family, value, _) = number(ip);
        let ranges = &self.ranges[family];
        let index = ranges.partition_point(|&(start, _)| start <= value);
        index > 0 && ranges[index - 1].1 >= value || self.learned(ip, now)
    }
    pub fn remember(&self, records: Vec<(IpAddr, u32)>, now: Instant) -> bool {
        const LIMIT: usize = 8192;
        let mut entries = self.learned.lock().unwrap();
        if entries.len() + records.len() > LIMIT {
            entries.retain(|_, end| *end > now);
            let new: HashSet<_> = records
                .iter()
                .map(|(ip, _)| ip)
                .filter(|ip| !entries.contains_key(ip))
                .collect();
            if entries.len() + new.len() > LIMIT {
                return false;
            }
        }
        for (ip, ttl) in records {
            entries.insert(ip, now + Duration::from_secs(ttl.clamp(30, 600).into()));
        }
        true
    }
}

/// Leading directive lines before the rule snapshot: an optional (debug) pair of
/// domestic resolvers, and VPN DNS addresses whose DoT port must be refused.
pub fn directives(mut text: &str) -> Option<(Vec<SocketAddr>, Vec<IpAddr>, &str)> {
    fn list<T: std::str::FromStr>(line: &str) -> Option<Vec<T>> {
        let items = line
            .split(',')
            .map(str::parse)
            .collect::<Result<Vec<T>, _>>()
            .ok()?;
        (1..=2).contains(&items.len()).then_some(items)
    }
    let mut resolvers = vec![
        SocketAddr::from(([223, 5, 5, 5], 53)),
        SocketAddr::from(([119, 29, 29, 29], 53)),
    ];
    let mut refuse_dot = Vec::new();
    loop {
        let (line, rest) = text.split_once('\n').unwrap_or((text, ""));
        if let Some(v) = line.strip_prefix("resolvers:") {
            resolvers = list(v)?;
        } else if let Some(v) = line.strip_prefix("refuse-dot:") {
            refuse_dot = list(v)?;
        } else {
            return Some((resolvers, refuse_dot, text));
        }
        text = rest;
    }
}

fn short(packet: &[u8], pos: usize) -> Option<u16> {
    Some(u16::from_be_bytes(
        packet.get(pos..pos + 2)?.try_into().ok()?,
    ))
}

// Limit both expanded wire length and pointer hops. Dots inside labels must never
// be mistaken for suffix boundaries by the domestic-domain matcher.
fn name(packet: &[u8], cursor: &mut usize) -> Option<String> {
    let mut pos = *cursor;
    let mut jumped = false;
    let mut out = String::new();
    for _ in 0..128 {
        let n = usize::from(*packet.get(pos)?);
        if n & 0xc0 == 0xc0 {
            let target = usize::from(short(packet, pos)? & 0x3fff);
            if target < 12 || target >= pos {
                return None;
            }
            if !jumped {
                *cursor = pos + 2;
                jumped = true
            }
            pos = target;
        } else {
            if n > 63 {
                return None;
            }
            pos += 1;
            if n == 0 {
                if !jumped {
                    *cursor = pos
                }
                return Some(out);
            }
            let label = packet.get(pos..pos + n)?;
            if !label
                .iter()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'-' | b'_'))
            {
                return None;
            }
            if !out.is_empty() {
                out.push('.')
            }
            out.extend(label.iter().map(|c| char::from(c.to_ascii_lowercase())));
            if out.len() > 253 {
                return None;
            }
            pos += n;
        }
    }
    None
}

fn question(packet: &[u8]) -> Option<(String, u16, usize)> {
    if short(packet, 4)? != 1 {
        return None;
    }
    let mut pos = 12;
    let qname = name(packet, &mut pos)?;
    if short(packet, pos + 2)? != 1 {
        return None;
    }
    Some((qname, short(packet, pos)?, pos + 4))
}

pub fn domestic_question(rules: &Rules, packet: &[u8]) -> bool {
    let valid = || {
        if short(packet, 2)? & 0xf800 != 0 || short(packet, 6)? != 0 || short(packet, 8)? != 0 {
            return None;
        }
        let (qname, _, mut pos) = question(packet)?;
        // Permit a single EDNS OPT. Reject compound queries and trailing records.
        match short(packet, 10)? {
            0 => (),
            1 if packet.get(pos) == Some(&0) && short(packet, pos + 1)? == 41 => {
                pos += 11 + usize::from(short(packet, pos + 9)?)
            }
            _ => return None,
        }
        Some(pos == packet.len() && rules.domain(&qname))
    };
    valid() == Some(true)
}

pub fn answers(query: &[u8], response: &[u8]) -> Option<Vec<(IpAddr, u32)>> {
    let (q, kind, _) = question(query)?;
    let (rq, rkind, mut pos) = question(response)?;
    if short(query, 0)? != short(response, 0)?
        || q != rq
        || kind != rkind
        || short(response, 2)? & 0xf800 != 0x8000
    {
        return None;
    }
    if short(response, 2)? & 0x020f != 0 {
        return Some(Vec::new());
    }
    let mut alias = HashMap::new();
    let mut addresses = Vec::new();
    for _ in 0..short(response, 6)? {
        let owner = name(response, &mut pos)?;
        let typ = short(response, pos)?;
        let class = short(response, pos + 2)?;
        let ttl = u32::from_be_bytes(response.get(pos + 4..pos + 8)?.try_into().ok()?);
        let n = usize::from(short(response, pos + 8)?);
        pos += 10;
        let data = response.get(pos..pos + n)?;
        if class == 1 {
            match typ {
                1 if kind == 1 || kind == 255 => addresses.push((
                    owner,
                    IpAddr::V4(Ipv4Addr::from(<[u8; 4]>::try_from(data).ok()?)),
                    ttl,
                )),
                28 if kind == 28 || kind == 255 => addresses.push((
                    owner,
                    IpAddr::V6(Ipv6Addr::from(<[u8; 16]>::try_from(data).ok()?)),
                    ttl,
                )),
                5 => {
                    let mut end = pos;
                    let cname = name(response, &mut end)?;
                    if end != pos + n {
                        return None;
                    }
                    alias.insert(owner, (cname, ttl));
                }
                _ => (),
            }
        }
        pos += n;
    }
    let mut owner = q;
    let mut limit = u32::MAX;
    for _ in 0..=alias.len() {
        match alias.get(&owner) {
            Some((next, ttl)) => {
                owner = next.clone();
                limit = limit.min(*ttl)
            }
            None => {
                return Some(
                    addresses
                        .into_iter()
                        .filter(|(s, _, _)| *s == owner)
                        .map(|(_, ip, ttl)| (ip, ttl.min(limit)))
                        .collect(),
                )
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    const QUERY: &[u8] = b"\0\x11\x01\0\0\x01\0\0\0\0\0\0\x03WWW\x04test\0\0\x01\0\x01";
    #[test]
    fn domains_and_cidrs() {
        let r = Rules::parse("domain:test\nfull:only.example\nip:192.0.2.0/24\nip:2001:db8::/127")
            .unwrap();
        assert!(r.domain("a.b.test") && r.domain("test") && r.domain("only.example"));
        assert!(!r.domain("contest") && !r.domain("a.only.example"));
        for (ip, want) in [
            ("192.0.2.0", true),
            ("192.0.2.255", true),
            ("192.0.3.0", false),
            ("2001:db8::1", true),
            ("2001:db8::2", false),
        ] {
            assert_eq!(r.direct(ip.parse().unwrap(), Instant::now()), want);
        }
    }
    #[test]
    fn ipv6_answer_follows_the_question_chain() {
        let mut q = QUERY.to_vec();
        let kind = q.len() - 3;
        q[kind] = 28;
        let mut response = q.clone();
        response[2] = 0x81;
        response[7] = 3;
        // www.test CNAME cdn.test, followed by an unrelated AAAA and the real AAAA.
        response.extend_from_slice(b"\xc0\x0c\0\x05\0\x01\0\0\0\x29\0\x06\x03cdn\xc0\x10");
        let ip: Ipv6Addr = "2001:db8::1".parse().unwrap();
        for owner in [b"\x03bad\xc0\x10", b"\x03cdn\xc0\x10"] {
            response.extend_from_slice(owner);
            response.extend_from_slice(b"\0\x1c\0\x01\0\0\x10\0\0\x10");
            response.extend(ip.octets());
        }
        assert_eq!(answers(&q, &response), Some(vec![(IpAddr::V6(ip), 41)]));
        response[0] ^= 1;
        assert!(answers(&q, &response).is_none());
        let rules = Rules::parse("domain:test").unwrap();
        q[5] = 2;
        assert!(!domestic_question(&rules, &q));
    }

    #[test]
    fn dns_and_expiry() {
        let r = Rules::parse("domain:test").unwrap();
        assert!(domestic_question(&r, QUERY));
        let mut response = QUERY.to_vec();
        response[2] = 0x81;
        response[7] = 1;
        response.extend_from_slice(b"\xc0\x0c\0\x01\0\x01\0\0\0\x01\0\x04\xc0\0\x02\x01");
        let records = answers(QUERY, &response).unwrap();
        let (ip, ttl) = records[0];
        assert_eq!(ttl, 1);
        let now = Instant::now();
        r.remember(records, now);
        assert!(r.learned(ip, now));
        assert!(!r.learned(ip, now + Duration::from_secs(30)));
        r.remember(vec![(ip, 86400)], now);
        assert!(!r.learned(ip, now + Duration::from_secs(600)));
        for n in 0..response.len() {
            assert!(answers(QUERY, &response[..n]).is_none())
        }
        let mut cyclic = QUERY[..12].to_vec();
        cyclic.extend_from_slice(b"\xc0\x0c\0\x01\0\x01");
        assert!(!domestic_question(&r, &cyclic));
        let mut long = QUERY[..12].to_vec();
        let mut label = [b'x'; 64];
        label[0] = 63;
        long.extend(label.repeat(4));
        long.extend_from_slice(b"\0\0\x01\0\x01");
        assert!(!domestic_question(&r, &long));
    }

    #[test]
    fn directive_lines() {
        let (r, d, rest) =
            directives("refuse-dot:1.1.1.1\nresolvers:192.0.2.1:5353\ndomain:test\n").unwrap();
        assert_eq!(r, vec!["192.0.2.1:5353".parse::<SocketAddr>().unwrap()]);
        assert_eq!(d, vec!["1.1.1.1".parse::<IpAddr>().unwrap()]);
        assert_eq!(rest, "domain:test\n");
        let (r, d, rest) = directives("domain:test").unwrap();
        assert_eq!((r.len(), d.len(), rest), (2, 0, "domain:test"));
        assert_eq!(directives("refuse-dot:1.1.1.1").unwrap().2, "");
        assert!(directives("refuse-dot:not-an-ip\ndomain:test").is_none());
        assert!(directives("resolvers:\ndomain:test").is_none());
        assert!(directives("refuse-dot:1.1.1.1,1.0.0.1,8.8.8.8\n").is_none());
    }
}
