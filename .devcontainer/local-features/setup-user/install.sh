#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License. See https://go.microsoft.com/fwlink/?linkid=2090316 for license information.
#-------------------------------------------------------------------------------------------------------------

USERNAME=${USERNAME:-"codespace"}

set -eux

if [ "$(id -u)" -ne 0 ]; then
    echo -e 'Script must be run as root. Use sudo, su, or add "USER root" to your Dockerfile before running this script.'
    exit 1
fi

# Ensure that login shells get the correct path if the user updated the PATH using ENV.
rm -f /etc/profile.d/00-restore-env.sh
echo "export PATH=${PATH//$(sh -lc 'echo $PATH')/\$PATH}" > /etc/profile.d/00-restore-env.sh
chmod +x /etc/profile.d/00-restore-env.sh

export DEBIAN_FRONTEND=noninteractive

sudo_if() {
    COMMAND="$*"
    if [ "$(id -u)" -eq 0 ] && [ "$USERNAME" != "root" ]; then
        su - "$USERNAME" -c "$COMMAND"
    else
        "$COMMAND"
    fi
}

# Enables the oryx tool to generate manifest-dir which is needed for running the postcreate tool
DEBIAN_FLAVOR="focal-scm"
mkdir -p /opt/oryx && echo "vso-focal" > /opt/oryx/.imagetype
echo "DEBIAN|${DEBIAN_FLAVOR}" | tr '[a-z]' '[A-Z]' > /opt/oryx/.ostype

# Oryx expects the tool to be installed at `/opt/oryx` and looks for relevant files in there.
ln -snf /usr/local/oryx/* /opt/oryx

# For the universal image, oryx build tool installs the detected platforms in /home/codespace/*. Hence, linking current platforms to the /home/codespace/ path and adding it to the PATH.
# This ensures that whatever platfornm versions oryx detects and installs are set as root.
NODE_PATH="/home/codespace/nvm/current"
ln -snf /usr/local/share/nvm /home/codespace

HOME_DIR="/home/${USERNAME}/"
chown -R ${USERNAME}:${USERNAME} ${HOME_DIR}
chmod -R g+r+w "${HOME_DIR}"
find "${HOME_DIR}" -type d | xargs -n 1 chmod g+s

OPT_DIR="/opt/"
chown -R ${USERNAME}:oryx ${OPT_DIR}
chmod -R g+r+w "${OPT_DIR}"
find "${OPT_DIR}" -type d | xargs -n 1 chmod g+s

echo "Defaults secure_path=\"${NODE_PATH}/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/bin:/usr/local/share:/home/${USERNAME}/.local/bin:${PATH}\"" >> /etc/sudoers.d/$USERNAME

# Temporary: Due to GHSA-c2qf-rxjj-qqgw
bash -c ". /usr/local/share/nvm/nvm.sh && nvm use 18"
bash -c "npm -g install -g npm@9.8.1"
bash -c ". /usr/local/share/nvm/nvm.sh && nvm use stable"

echo "Done!"
