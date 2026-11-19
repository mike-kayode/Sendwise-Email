#!/usr/bin/env python3
import re
import sys
from pathlib import Path


root = Path(__file__).resolve().parent.parent


def service_environment(path: Path) -> dict[str, set[str]]:
    services: dict[str, set[str]] = {}
    in_services = False
    service = None
    in_environment = False
    for line in path.read_text().splitlines():
        if re.match(r"^services:\s*$", line):
            in_services = True
            service = None
            in_environment = False
            continue
        if in_services and re.match(r"^[A-Za-z][A-Za-z0-9_-]*:\s*$", line):
            in_services = False
            service = None
            in_environment = False
        if not in_services:
            continue
        service_match = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", line)
        if service_match:
            service = service_match.group(1)
            services.setdefault(service, set())
            in_environment = False
            continue
        if service is None:
            continue
        if re.match(r"^    environment:\s*$", line):
            in_environment = True
            continue
        if in_environment:
            if re.match(r"^    [A-Za-z0-9_-]+:", line) or re.match(r"^  [A-Za-z0-9_-]+:", line):
                in_environment = False
                continue
            variable = re.match(r"^      ([A-Z][A-Z0-9_]*):", line)
            if variable:
                services[service].add(variable.group(1))
    return services


def config_keys(path: Path) -> set[str]:
    text = path.read_text()
    return set(re.findall(r'(?:getenv|kit\.Getenv[A-Za-z]*)\(\s*(?:lookup,\s*)?"([A-Z][A-Z0-9_]+)"', text))


compose = service_environment(root / "docker-compose.yml")
microservices = service_environment(root / "docker-compose.microservices.yml")
for service, keys in microservices.items():
    compose.setdefault(service, set()).update(keys)

base_keys = config_keys(root / "internal/kit/config.go")
sources = {
    "api": base_keys | config_keys(root / "internal/config/config.go"),
    "worker": base_keys | config_keys(root / "internal/config/config.go"),
    "identity": base_keys | config_keys(root / "services/identity/config.go"),
    "workspace": base_keys | config_keys(root / "services/workspace/config.go"),
    "gateway": base_keys | config_keys(root / "services/gateway/config.go"),
}
missing = []
for service, required in sources.items():
    for key in sorted(required - compose.get(service, set())):
        missing.append(f"{service}: {key}")

if missing:
    print("Go configuration keys missing from Compose service environments:", file=sys.stderr)
    print("\n".join(f"  {item}" for item in missing), file=sys.stderr)
    sys.exit(1)
print("Compose environment covers all Go configuration keys.")
