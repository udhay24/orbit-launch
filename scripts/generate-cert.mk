sudo certbot certonly --manual --preferred-challenges=dns --email your-email@example.com --server https://acme-v02.api.letsencrypt.org/directory -d s2tlive.com -d *.s2tlive.com

sudo certbot certonly --manual --preferred-challenges=dns -d s2tlive.com -d www.s2tlive.com


sudo cp /etc/letsencrypt/live/s2tlive.com-0003/fullchain.pem server.crt

sudo cp /etc/letsencrypt/live/s2tlive.com-0003/privkey.pem server.key