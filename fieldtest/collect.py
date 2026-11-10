#!/usr/bin/env python3
"""Turn a Workflow journal.jsonl into a round file: one object per run with
source and model taken from the agent label (e.g. "telegram-haiku").

Usage: python3 fieldtest/collect.py <journal.jsonl> > fieldtest/roundN.json
"""
import json, sys

labels, out = {}, []
for line in open(sys.argv[1]):
    d = json.loads(line)
    if d["type"] == "started":
        labels[d["key"]] = d["label"]
    elif d["type"] == "result" and isinstance(d.get("result"), dict):
        src, model = labels[d["key"]].rsplit("-", 1)
        out.append({"source": src, "model": model, **d["result"]})
json.dump(sorted(out, key=lambda r: (r["source"], r["model"])), sys.stdout, indent=1, ensure_ascii=False)
