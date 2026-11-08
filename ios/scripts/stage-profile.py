#!/usr/bin/env python3
"""Stage a registered native variant without modifying the source checkout or signing."""
import argparse
from pathlib import Path
import subprocess
import sys
from native_profile import stage, read_profile

if __name__ == "__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile",required=True)
    parser.add_argument("--output-directory")
    parser.add_argument("--validate-only",action="store_true")
    args=parser.parse_args()
    try:
        if args.validate_only:
            if args.output_directory: raise ValueError("validation does not accept an output directory")
            read_profile(args.profile)
        else:
            if not args.output_directory: raise ValueError("staging requires --output-directory")
            root=stage(args.profile,args.output_directory)
            subprocess.run([sys.executable,str(root/"scripts/generate-project.py")],check=True)
    except (ValueError,OSError,subprocess.CalledProcessError):
        print("Native profile validation or staging failed; no app was signed or deployed.",file=sys.stderr);sys.exit(1)
