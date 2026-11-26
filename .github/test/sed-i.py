#!/usr/bin/python3
import os
import re
import sys

args = sys.argv[1:]
if "-i" not in args:
    os.execv("/usr/bin/sed", ["sed"] + args)
scripts, files, i = [], [], 0
while i < len(args):
    if args[i] == "-i":
        i += 1
    elif args[i] == "-e":
        scripts.append(args[i + 1])
        i += 2
    else:
        files.append(args[i])
        i += 1
for f in files:
    lines = open(f).read().split("\n")
    for s in scripts:
        if s.startswith("s|"):
            _, a, b, _ = s.split("|")
            lines = [line.replace(a, b) for line in lines]
        elif s.endswith("/d"):
            lines = [line for line in lines if not re.search(s[1:-2], line)]
        else:
            m = re.match(r"/(.+)/a (.*)$", s)
            out = []
            for line in lines:
                out.append(line)
                if re.search(m.group(1), line):
                    out.append(m.group(2))
            lines = out
    open(f, "w").write("\n".join(lines))
