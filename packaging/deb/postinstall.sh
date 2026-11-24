#!/bin/sh
set -e

# Instance env files live here, one per chain.
mkdir -p /etc/cosmos-docker-upgrader

# The unit is a template, instances are enabled per chain by the operator, so
# nothing is started here. Only make systemd aware of the unit file.
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi

exit 0
