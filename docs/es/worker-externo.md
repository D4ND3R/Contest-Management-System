# Agregar un worker en otra máquina

Un worker compila y ejecuta los envíos. El servidor principal ya ejecuta
uno (en su núcleo de evaluación); para un concurso grande agrega máquinas
que solo evalúen. Un worker remoto necesita dos cosas del servidor
principal:

- **Valkey** (puerto 6379): las colas de trabajos;
- el **servidor de blobs** (puerto 8891, `cms blob-server`): casos de
  prueba, managers y envíos, y un lugar donde subir ejecutables y salidas.

Nunca habla con PostgreSQL. Ambos puertos llevan datos del concurso y solo
deben ser accesibles por una red privada. Lo más simple es WireGuard entre
las máquinas; también sirve la red privada (VPC) del proveedor.

## 1. Red privada (WireGuard)

En ambas máquinas: `sudo apt-get install -y wireguard` y
`wg genkey | tee private.key | wg pubkey > public.key`.

Servidor principal, `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = <clave privada del principal>

[Peer]           # un bloque por worker
PublicKey = <clave pública del worker>
AllowedIPs = 10.8.0.2/32
```

Worker, `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.8.0.2/24
PrivateKey = <clave privada del worker>

[Peer]
PublicKey = <clave pública del principal>
Endpoint = cms.example.org:51820
AllowedIPs = 10.8.0.1/32
PersistentKeepalive = 25
```

En ambas: `sudo systemctl enable --now wg-quick@wg0`; en el principal
además `sudo ufw allow 51820/udp`. Comprueba con `ping 10.8.0.1` desde el
worker.

## 2. Servidor principal: escuchar en la dirección privada

```sh
sudo bash /opt/cms/current/scripts/install.sh --domain cms.example.org --private-ip 10.8.0.1   # mismas opciones que antes
```

Así Valkey también escucha en 10.8.0.1, se configura `blob_server.listen:
10.8.0.1:8891` (en un `cms.yaml` escrito antes, agrégalo a mano), se activa
`cms-blob-server` y se abren 6379 y 8891 solo en 10.8.0.1. Anota
`REDIS_PASSWORD` y `BLOB_TOKEN` de `/etc/cms/secrets.env`.

## 3. Máquina del worker

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh | sudo bash -s -- \
     --role worker --version <la versión del servidor principal: cms version> --main 10.8.0.1 \
     --redis-password <REDIS_PASSWORD> --blob-token <BLOB_TOKEN> --worker-name juez-2
```

El instalador termina corriendo `cms-verify-host`, que debe pasar. Mantén
cada worker en la versión del servidor principal: después de `sudo cmsctl
upgrade` en el servidor principal, corre `sudo cmsctl upgrade -version <la
misma>` en cada worker (un worker no tiene base de datos: solo cambia la
versión y reinicia).

El `cms.yaml` del worker usa `redis.url: redis://:<contraseña>@10.8.0.1:6379/0`
y el backend de blobs `http` (`blob.http.url: http://10.8.0.1:8891`) con una
caché local de casos de prueba de 4 GiB; evalúa en todas las CPUs salvo la
primera. Solo se activa `cms-worker` y el firewall solo permite SSH.

En segundos el worker aparece en el admin en **Workers y colas** y empieza a
tomar trabajos. Da a cada worker un `--worker-name` distinto.

## Notas

- Hardware: todas las máquinas de evaluación deberían tener el mismo modelo
  de CPU y la misma configuración, o una misma solución puede tardar
  distinto en workers distintos. `cms-verify-host` avisa sobre turbo, SMT y
  escalado de frecuencia; corrígelos en máquinas dedicadas.
- Un worker que se detiene (caída, red, reinicio) no pierde nada: pasado
  `monitor.heartbeat_timeout` sus trabajos vuelven a la cola y otro worker
  los toma.
- Para detener un worker por mantenimiento: `sudo systemctl stop cms-worker`
  (primero termina los trabajos en curso; lo que quede vuelve a la cola).
- Los blobs que lee un worker se verifican contra su SHA-256; el servidor de
  blobs solo acepta el token y nunca borra nada.
