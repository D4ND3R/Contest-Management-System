# Problemas frecuentes

Síntomas comunes, su causa y qué hacer. Si el problema no está aquí, revisa
**Jueces** (estado de los jueces y las colas) y **Registro de auditoría** en
la administración, y los registros de los servicios
(`journalctl -u 'cms-*'` en una instalación con systemd, `docker compose
logs` con Docker).

## Las páginas no se actualizan solas

Las páginas reciben novedades por una conexión abierta (*Server-Sent
Events*). Si un proxy la corta o la almacena, la página lo nota en un minuto
y cambia sola a consultar cada 8 segundos, así que los resultados llegan
igual, un poco más tarde.

- Con Caddy o nginx delante, la ruta `/<concurso>/events` no debe
  comprimirse ni almacenarse en búfer. En nginx: `proxy_buffering off;` y
  `proxy_read_timeout 1h;` para esa ruta. El Caddyfile del instalador ya lo
  hace.
- Un antivirus o proxy corporativo en las computadoras de los concursantes
  puede bloquear la conexión: el modo de consulta sigue funcionando.

## Los envíos se quedan en "Compilando…" o "Evaluando"

- **Jueces**: ¿hay jueces *vivos*? Si no, inicia el servicio del juez
  (`systemctl start cms-worker` o el contenedor `worker`).
- Si hay jueces pero la cola crece, son pocos para la carga: agrega jueces
  ([worker externo](worker-externo.md)).
- Un trabajo *atascado* (más tiempo que el límite) se reencola solo; también
  puedes **reencolar** a mano desde Jueces.
- Un envío con **error del sistema** (la página del concursante dice *La
  evaluación falló (se avisó a los organizadores)*) aparece en el Panel;
  **reevalúalo** cuando el problema esté corregido.

## Todos los envíos dan error del sistema

Casi siempre es el sandbox: ejecuta la verificación de la máquina
([verificar el host](verificar-host.md)) y `cmsctl judge-selftest`. Revisa
que `isolate` esté instalado y que el directorio de trabajo del juez exista.

## Las horas se ven mal

Todas las horas se muestran en la **zona horaria del servidor**
(**Servidor** en la administración); el pie de cada página dice cuál es.
Cámbiala ahí: el concurso, los rankings y el panel la siguen al instante.
Un usuario puede tener su propia zona (concursantes remotos) en su página.

## Un concursante no puede entrar

- ¿El concurso está *publicado* y el usuario inscrito (**Participantes**)?
- ¿Tiene restricción por IP y entra desde otra dirección?
- ¿Está bloqueado por sesiones simultáneas (otra computadora abierta)?
- Genera una contraseña nueva en su participación.

## El problema no da el puntaje esperado

- La lista **Preparación** del problema dice qué falta.
- Envía la solución de referencia con el **Probador de problemas** y abre el
  envío: el detalle por caso dice qué falla.
- Si cambiaste casos o subtareas, **reevalúa** (página del dataset).

## Una actualización falló con un error de la base de datos

`cmsctl upgrade` toma un respaldo antes de migrar y vuelve a la versión
anterior si algo falla. Guarda el mensaje completo y el archivo de
respaldo; ver [despliegue](despliegue.md#actualizar).
