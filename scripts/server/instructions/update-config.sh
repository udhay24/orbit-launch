#!/bin/bash

# Change directory to /home/ubuntu/orbit-play
cd /home/ubuntu/orbit-play || { echo "Failed to cd into /home/ubuntu/orbit-play"; exit 1; }

# Stop the orbit-play service
sudo systemctl stop orbit-play.service || { echo "Failed to stop orbit-play.service"; exit 1; }

# Remove the old mediamtx and mediamtx.yml files
rm -f mediamtx.yml mediamtx.log || { echo "Failed to remove old files"; exit 1; }

# Download the new mediamtx and mediamtx.yml files
wget https://github.com/udhay24/orbit-launch/releases/download/v1/mediamtx.yml || { echo "Failed to download mediamtx.yml"; exit 1; }

# Restart the orbit-play service
sudo systemctl restart orbit-play.service || { echo "Failed to restart orbit-play.service"; exit 1; }

echo "Script executed successfully!"