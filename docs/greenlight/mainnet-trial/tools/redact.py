#!/usr/bin/env python3
"""redact.py — scrub a log or JSON file before it goes into a public evidence report.

Usage: python3 redact.py < input > output
Removes: NWC connection secrets, bearer/JWT tokens, PEM blocks, preimages, bolt11 invoices
(kept as a short prefix), IP addresses, home-directory paths and usernames, hostnames of the
form *.local; converts timestamps with a UTC offset (e.g. +01:00) to UTC 'Z' so no local
timezone is revealed. Keeps payment hashes, node ids and txids (needed to verify the report).
Always read the output yourself before publishing: this is a safety net, not a guarantee.
"""
import re, sys, datetime, getpass, socket

text = sys.stdin.read()

def to_utc(m):
    s = m.group(0)
    try:
        dt = datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))
        return dt.astimezone(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    except ValueError:
        return "[time]"

rules = [
    (r"nostr\+walletconnect://[^\s\"'<>]+", "[NWC-SECRET]"),
    (r"-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----", "[PEM]"),
    (r"(?i)(bearer\s+)[A-Za-z0-9._\-]+", r"\1[TOKEN]"),
    (r"eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+", "[JWT]"),
    (r"(?i)(\"?(?:payment_)?preimage\"?\s*[:=]\s*\"?)[0-9a-f]{64}", r"\1[PREIMAGE]"),
    (r"(?i)(\"?(?:secret|password|unlockPassword|mnemonic|token)\"?\s*[:=]\s*\")[^\"]*", r"\1[REDACTED]"),
    (r"\b(ln(?:bc|tb|bcrt|tbs)[0-9]*[a-z]?1)[02-9ac-hj-np-z]{40,}", r"\1…[invoice]"),
    (r"\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b", "[ip]"),
    (r"(/home|/Users)/[^/\s\"']+", r"\1/[user]"),
    (r"C:\\\\Users\\\\[^\\\s\"']+", r"C:\\Users\\[user]"),
    (r"\b[\w\-]+\.local\b", "[host]"),
]
for pat, rep in rules:
    text = re.sub(pat, rep, text, flags=re.S)
text = re.sub(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?[+-]\d{2}:\d{2}", to_utc, text)
for word in {getpass.getuser(), socket.gethostname().split(".")[0]}:
    if len(word) >= 3:
        text = re.sub(re.escape(word), "[redacted]", text)
sys.stdout.write(text)
