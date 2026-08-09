#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Malaxis Fleet - node agent launcher (v1.1.0).

Thin bootstrap that exposes the modular agent_src package through the legacy
`import node_agent` API used by fleet-cli.sh / fleet-cli.ps1
(`docker exec node-agent python3 -c "import node_agent; ..."`) and runs the
agent daemon when executed directly by entrypoint.sh.

The full implementation lives in the agent_src/ package, which is distributed
as agent_src.zip and updated via the OTA update_client_files flow.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from agent_src.main import *  # noqa: F401,F403
from agent_src.main import main  # noqa: F401

if __name__ == "__main__":
    main()
