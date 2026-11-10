#!/usr/bin/env python3
"""Score a docli field-test round: answers vs known values, add result, budget.

Usage: python3 fieldtest/score.py fieldtest/round1.json [--md]
"""
import json, re, sys

# Expected answers, checked against the live docs on 2026-11-10. Each entry is a
# list of regexes that must all match (case-insensitive) the agent's answer.
EXPECT = {
    "telegram": [[r"chat_id", r"\btext\b"], [r"1\s*-\s*64\s*bytes|1 to 64 bytes"], [r"\b0\b"]],
    "datastar": [[r"one second|1\s*s(econd)?\b|1000\s*ms"], [r"\b10\b"], [r"false"]],
    "antd":     [[r"2000"], [r"middle"], [r"solid", r"outline", r"none"]],
}
BUDGET = 5000

def score(run):
    exp = EXPECT[run["source"]]
    answers = run.get("answers", [])
    correct = []
    for i, pats in enumerate(exp):
        a = answers[i]["answer"] if i < len(answers) else ""
        correct.append(bool(a) and "not found" not in a.lower() and all(re.search(p, a, re.I) for p in pats))
    over = [a.get("tokens_read", 0) for a in answers if a.get("tokens_read", 0) > BUDGET]
    ok = run.get("add_ok") and all(correct) and not over and run.get("budget_kept")
    return correct, over, ok

def main():
    runs = json.load(open(sys.argv[1]))
    md = "--md" in sys.argv
    if md:
        print("| source | model | add | answers | max tokens/question | problems (blocker/wrong/friction) | pass |")
        print("|---|---|---|---|---|---|---|")
    total = 0
    for r in runs:
        correct, over, ok = score(r)
        total += ok
        sev = [p["severity"] for p in r.get("problems", [])]
        counts = "/".join(str(sev.count(s)) for s in ("blocker", "wrong", "friction"))
        mx = max([a.get("tokens_read", 0) for a in r.get("answers", [])] or [0])
        if md:
            print(f"| {r['source']} | {r['model']} | {'ok' if r.get('add_ok') else 'failed'} | {sum(correct)}/3 | {mx} | {counts} | {'pass' if ok else 'FAIL'} |")
        else:
            print(f"{r['source']:9} {r['model']:7} add={'ok ' if r.get('add_ok') else 'NO '} answers={sum(correct)}/3 {['x','✓'][0] if False else ''}{''.join('✓' if c else '✗' for c in correct)} maxtok={mx:5} problems={counts} {'PASS' if ok else 'FAIL'}")
    print(f"\n{total}/{len(runs)} runs pass")
    sys.exit(0 if total == len(runs) else 1)

if __name__ == "__main__":
    main()
