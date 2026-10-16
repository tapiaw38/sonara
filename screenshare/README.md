# Compartir pantalla sin exponer Sonara (Mango + un solo monitor)

Problema: tenés un solo monitor físico (`eDP-2`). Al compartir pantalla completa
en Meet/Teams, Sonara —que vive en ese mismo monitor— queda visible para el
otro lado de la llamada.

Solución: un output virtual (`HEADLESS-*`, Mango lo nombra `HEADLESS-1` solo la
primera vez de la sesión — ver [limitación de nombrado](#limitación-conocida-el-nombre-custom-no-es-confiable))
que Mango ya sabe crear de forma nativa. Lo que se comparte en la llamada es
*esa* salida invisible, no la pantalla real. Vos ves en vivo qué hay ahí
mediante un visor VNC local, y Sonara sigue flotando por encima, solo en
`eDP-2`, donde nadie remoto la ve.

```
┌─────────────── eDP-2 (pantalla real, nadie remoto la ve) ───────────────┐
│  escritorio normal · ventana del visor VNC (contenido del output virtual)│
│  Sonara flotando arriba de todo (overlay, SUPER+o)                      │
└───────────────────────────────────────────────────────────────────────┘
                              ▲ wayvnc (solo 127.0.0.1) captura
┌────────────── output virtual HEADLESS-N (invisible) ────────────────────┐
│  lo que decidas que viva ahí — eso es lo único que ve Meet/Teams         │
└───────────────────────────────────────────────────────────────────────┘
```

El portal (`xdg-desktop-portal-wlr`) puede configurarse para que **toda**
petición de screencast vaya directo al output virtual, sin picker: no hay
forma interactiva de elegir `eDP-2` por error — con el matiz de nombrado de
la sección de abajo.

## Estado: compilado y verificado end-to-end, sin GUI real todavía probada

| Pieza | Estado |
|---|---|
| Crear/destruir output virtual (Mango nativo) | ✅ verificado en vivo, repetido |
| `eDP-2` intacta durante todo el proceso | ✅ verificado |
| `wayvnc` compilado sin ffmpeg, en `~/.local` | ✅ compilado, instalado, corrido |
| `wayvnc` sirviendo el output virtual (handshake RFB real) | ✅ verificado con socket crudo |
| `wayvncctl output-list` confirma que captura solo el virtual, nunca `eDP-2` | ✅ verificado |
| Visor VNC real (ventana abierta, contenido visible) | ❌ no abierto — ver nota abajo |
| Config de portal (`output_name`, `chooser_type=none`) | ✅ escrita, no copiada (ver limitación de nombrado) |
| Sonara overlay por encima del visor | ✅ ya existe (botón in-app / `SUPER+o`) |
| Prueba real con un Meet/Teams en vivo | ❌ pendiente — fuera de alcance sin vos presente |

**Por qué no se abrió el visor en esta verificación:** `gvncviewer` es una
ventana GTK real — abrirla hubiera puesto algo visible en tu pantalla sin que
estuvieras mirando. En su lugar se verificó el protocolo VNC a nivel de
socket crudo (`RFB 003.008` recibido de `wayvnc` en vivo) y con
`wayvncctl output-list`, que es prueba de que el servidor funciona sin
necesidad de una GUI. Abrir `start.sh` de verdad y ver la ventana queda para
vos.

## Cómo funciona, verificado

### 1. El output virtual es una feature nativa de Mango, no un hack

Mango (`mangowm-git 0.16.3.r11.g4e6d7cb-1`, wlroots 0.20) crea **siempre**,
sin flags ni variables de entorno, un backend headless secundario además del
real (`src/main.c`). Expone dos comandos IPC dedicados (`src/dispatch/bind.c`):

- `create_virtual_output[,<nombre>]` — crea un output headless de 1920×1080.
- `destroy_all_virtual_output` — destruye todos los outputs headless activos.

No se tocó ni se va a tocar ningún archivo de `~/.config/mango`. Esto es usar
`mmsg` (la misma herramienta que ya usa Sonara para el always-on-top en
`app.go`), no modificar el compositor.

### 2. Limitación conocida: el nombre custom no es confiable

Verificado, reproducible, no es timing (probado con delays hasta 1.5s):

```
$ mmsg dispatch create_virtual_output,HEADLESS-1   # primera vez en la sesión
{"success":true}
$ wlr-randr | grep -E '^[A-Za-z]'
HEADLESS-1 "Headless output 1"       ← el nombre pedido se aplicó

$ mmsg dispatch destroy_all_virtual_output
$ mmsg dispatch create_virtual_output,HEADLESS-1   # segunda vez
{"success":true}
$ wlr-randr | grep -E '^[A-Za-z]'
HEADLESS-2 "Headless output 2"       ← ignoró el nombre pedido

# ... repetido 3 veces más en esta sesión: HEADLESS-3, HEADLESS-4, HEADLESS-5
```

El nombre pedido solo se aplica confiablemente la **primera vez** que se crea
un output virtual en la vida del proceso de Mango. Después de un
`destroy_all_virtual_output`, cada creación subsiguiente cae al contador
interno de wlroots en vez de honrar el nombre pedido. No se investigó la
causa exacta en el código de Mango (está fuera de alcance — "no modificar
MangoWM"); se trata como comportamiento dado y se maneja desde los scripts.

**Consecuencia práctica:** `start.sh` **no asume** que el output se llama
`HEADLESS-1`. Después de crearlo, compara la lista de outputs `HEADLESS-*`
antes/después (`comm -13`) y usa el nombre real que haya quedado, sea cual
sea. Si no coincide con lo pedido, imprime un aviso explícito con el nombre
real. **El portal, en cambio, usa un nombre estático en su archivo de
config** — si reiniciás varias veces en la misma sesión de Mango sin
reiniciar sesión, el portal puede quedar apuntando a un nombre que ya no
existe. Si eso pasa, actualizá `output_name` en
`~/.config/xdg-desktop-portal-wlr/config` al nombre real que te diga
`start.sh`, y reiniciá el servicio del portal.

### 3. Resolución

El output virtual nace en 1920×1080, que es exactamente el modo nativo de
`eDP-2` (`1920x1080 px, 144Hz`, confirmado con `wlr-randr`). No hace falta
ajustar nada.

### 4. El portal: por qué nunca puede elegir `eDP-2`

`xdg-desktop-portal-wlr` (0.8.1, ya corriendo como servicio de usuario) lee
`~/.config/xdg-desktop-portal-wlr/config` (hoy no existe — ver `portal/config`
en este directorio):

```ini
[screencast]
output_name=HEADLESS-1
chooser_type=none
```

Con `chooser_type=none`, el daemon **nunca muestra un selector**: sirve
directo el output de `output_name`, sin intervención humana posible — con el
matiz de que `output_name` es un string estático en un archivo, y el nombre
real puede no ser literalmente `HEADLESS-1` (sección anterior). Mientras el
output exista con ese nombre exacto, es imposible compartir `eDP-2` por
error: no es una opción en el flujo.

Instalación (no ejecutada, pendiente de que decidas activarla):

```sh
mkdir -p ~/.config/xdg-desktop-portal-wlr
cp screenshare/portal/config ~/.config/xdg-desktop-portal-wlr/config
systemctl --user restart xdg-desktop-portal-wlr.service
```

### 5. Foco y atajos — ya resuelto, sin cambios

Sonara ya implementa "always on top" como **overlay** de Mango
(`app.go: ToggleAlwaysOnTop`, dispatcha `toggleoverlay`), y Mango ya tiene el
atajo `SUPER+o` bindeado a esa misma función (`~/.config/mango/bind.conf:52`).
Un overlay se dibuja por encima de todo en el stacking *local* de `eDP-2`.
Esto no tiene ningún efecto sobre lo que `wayvnc` captura del output virtual
—son protocolos completamente distintos— así que Sonara puede estar tapando
al visor VNC en tu pantalla sin que eso cambie un solo pixel de lo que se
comparte.

### 6. Latencia

No medida con un visor real abierto todavía (ver "Estado" arriba). Lo que sí
se midió: el servidor acepta y responde el handshake RFB en menos de 1
segundo desde que arranca. Al ser todo loopback en la misma máquina, la
expectativa razonable sigue siendo "un par de frames", pero no se promete sin
medirlo con una ventana real.

## Instalación: compilado local, sin pacman

### Por qué no es `pacman -S wayvnc`

El paquete `wayvnc` de `extra` liga `neatvnc` con su encoder H.264 opcional
activado, que arrastra `ffmpeg` 9.0.2. Ese ffmpeg rompe ABI con paquetes ya
instalados (`chromaprint`, `thunderbird`, `wf-recorder`, y de ahí
`vlc-plugin-ffmpeg`, cadena que sigue creciendo). Confirmado con pacman
sincronizado (no es caché vieja):

```
error: no se pudo preparar la operación (no se pudieron satisfacer las dependencias)
:: instalando ffmpeg (2:9.0.2-2) se rompe la dependencia con «libavcodec.so=62-64», necesaria para el paquete chromaprint
...
```

Probado y descartado como atajo: `tigervnc` (depende de ffmpeg directo,
mismo bloqueo), `remmina`/`gtk-vnc` (instalan limpio pero son solo cliente,
no hay servidor VNC alternativo empaquetado), `yay -S wayvnc` (mismo paquete
de `extra`, sin variante AUR). La única salida vía pacman es
`sudo pacman -Syu` completo — explícitamente descartado por decisión propia.

### La solución real: compilar sin el encoder H.264

`neatvnc` tiene el H.264 (y por lo tanto ffmpeg) como **opción**, no como
dependencia dura: `meson_options.txt` define `option('h264', type: 'feature',
value: 'auto', ...)`, y las líneas `dependency('libavcodec', required:
get_option('h264'))` etc. en su `meson.build` solo buscan ffmpeg cuando esa
opción está habilitada. Compilando nosotros mismos con `-Dneatvnc:h264=disabled`,
ffmpeg nunca se prueba ni se necesita. Las demás codificaciones (ZRLE, Tight,
raw) se compilan igual, sin condición.

El mecanismo de vendoring es el que `wayvnc` ya trae, no uno inventado: su
propio `meson.build` hace

```meson
neatvnc_project = subproject('neatvnc', required: false, version: neatvnc_version)
aml_project = subproject('aml', required: false, version: aml_version)
```

con fallback a pkg-config si no hay subproject. Dropear `neatvnc` y `aml`
(con sus `.wrap` apuntando a los tags exactos que ese `meson.build` pide —
`>=1.0.0, <2.0.0` para ambos, verificado leyendo el `meson.build` real en el
tag `v0.10.2`, no en `master`) en `wayvnc/subprojects/` es exactamente la
forma en la que ese `meson.build` espera recibirlos.

**Verificado en esta máquina, compilación completa, cero errores:**

```sh
./screenshare/build-wayvnc.sh
# ...
# ninja: 148/148 pasos, sin errores
# build-wayvnc: instalado en /home/tapia/.local/bin/wayvnc
# build-wayvnc: confirmado — sin ffmpeg, todas las libs resuelven con LD_LIBRARY_PATH=...
```

Dependencias de build: **ninguna nueva**. Todo lo que `wayvnc`/`neatvnc`/`aml`
necesitan (menos ffmpeg) ya estaba instalado y resuelve vía `pkg-config`:
`pixman-1`, `xkbcommon`, `wayland-client`, `jansson`, `libdrm`, `gbm`, `zlib`,
`gnutls`, `nettle`, `hogweed`, `gmp`, `wayland-protocols`, más `meson`
`ninja` `git` `pkgconf` `gcc` `wayland-scanner` ya presentes. Único faltante:
`scdoc` (solo para man pages, deshabilitado con `-Dman-pages=disabled`, no
afecta el binario).

### Detalle que rompió en la primera verificación: `LD_LIBRARY_PATH`

`aml` se instala como `.so` compartido (no estático: `default_library`
default de meson es `shared`, no `static`, así que la condición de aml para
auto-excluirse de instalar no aplicó). Tanto `libaml.so.1` como
`libneatvnc.so.1` quedan en `~/.local/lib`, que **no** está en el search path
del linker dinámico por defecto. El binario recién compilado murió
instantáneo con `cannot open shared object file: No such file or directory`
hasta corregirlo. `start.sh`/`stop.sh` ya exportan
`LD_LIBRARY_PATH=$HOME/.local/lib:...` cuando usan el binario local; para
correr `wayvnc` a mano fuera de esos scripts, hace falta lo mismo:

```sh
export LD_LIBRARY_PATH="$HOME/.local/lib:$LD_LIBRARY_PATH"
```

### Comando

```sh
./screenshare/build-wayvnc.sh
```

Reversible por completo: para deshacer, `rm -rf ~/.local/bin/wayvnc*
~/.local/lib/libaml* ~/.local/lib/libneatvnc* ~/.local/include/aml.h
~/.local/include/neatvnc.h ~/.local/lib/pkgconfig/{aml1,neatvnc}.pc` (o
simplemente ignorarlos — no interfieren con nada del sistema).

## Uso

```sh
./screenshare/start.sh   # crea el output virtual, levanta wayvnc, abre el visor
# ... presentás dentro de la ventana del visor, compartís ese output en Meet/Teams ...
# cerrás la ventana del visor (o Ctrl+C en la terminal) y se limpia todo solo
```

`start.sh` prefiere `vncviewer` (tigervnc) si existe, y usa `gvncviewer`
(gtk-vnc, ya instalado en este sistema sin conflicto de ffmpeg) como
alternativa. Sus sintaxis de dirección son distintas —
`vncviewer host::puerto` vs `gvncviewer host:display` (display 0 = puerto
5900) — el script ya lo maneja.

Si algo quedó colgado:

```sh
./screenshare/stop.sh    # idempotente, no rompe nada si ya no hay nada corriendo
```

## Limitaciones — léelas antes de confiar en esto en una llamada real

1. **No probado contra un Meet/Teams real**, ni con un visor VNC abierto de
   verdad (ver "Estado" arriba). Verificado: creación/destrucción del output,
   `wayvnc` compilado y corriendo sin ffmpeg, handshake RFB real,
   `wayvncctl output-list` confirmando que captura el output correcto y
   nunca `eDP-2`. Antes de una entrevista o reunión real: corré `start.sh` de
   verdad, confirmá que el visor muestra lo que esperás, y hacé una llamada
   de prueba para ver qué ve el otro lado.
2. **El nombre del output no es predecible** después del primer uso en una
   sesión de Mango (sección 2 arriba). Si activaste el portal con
   `output_name` estático, puede necesitar actualización manual tras varios
   ciclos de uso sin reiniciar sesión.
3. **`chooser_type=none` es todo o nada.** Mientras el portal esté así
   configurado, *cualquier* app que pida screencast por este portal va al
   output fijado, sin preguntar.
4. **Orden de operaciones importa.** Si compartís desde Meet/Teams antes de
   correr `start.sh`, probablemente falle — no hay fallback con
   `chooser_type=none`.
5. **Latencia con visor real sin medir** — ver sección 6 arriba.
6. **No es diarización.** Esto resuelve qué ve el otro lado de tu pantalla.
   No tiene nada que ver con quién escucha Sonara en el micrófono.
