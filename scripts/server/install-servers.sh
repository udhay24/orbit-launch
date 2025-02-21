#!/bin/bash

# Define the base server name and range
BASE_SERVER="e"
START=1
END=2
SSH_KEY="/Users/udhay/Downloads/orbit-play1.pem"
USER="ubuntu"

# Loop through the servers
for i in $(seq $START $END); do
    SERVER="${BASE_SERVER}${i}.s2tlive.com"
    echo "Connecting to server: $SERVER"

    # SSH into the server and execute the commands
    ssh -i $SSH_KEY $USER@$SERVER -o StrictHostKeyChecking=no << 'EOF'
        echo "Connected to $(hostname)"
        echo "Executing install.sh script..."
        curl -sSL https://github.com/udhay24/orbit-launch/releases/download/1.2/install.sh | bash
        echo "Script execution completed on $(hostname)"
EOF

    # Check if the SSH command was successful
    if [ $? -eq 0 ]; then
        echo "Successfully executed commands on $SERVER"
    else
        echo "Failed to execute commands on $SERVER"
    fi

    # Sleep for 30 seconds before connecting to the next server
    echo "Sleeping for 30 seconds before connecting to the next server..."
    sleep 30
done

echo "Script execution completed for all servers."