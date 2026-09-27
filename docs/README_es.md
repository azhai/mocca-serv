<div align="center">
  <img src="../web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca es un servidor multimedia autoalojado para tu NAS: una API REST con streaming por rangos de bytes y, opcionalmente, una consola de administración integrada.</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="Repositorio" /></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="Licencia: AGPL-3.0" /></a>
</div>

---

- [English](../README.md) | [中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Deutsch](./README_de.md) | [Nederlands](./README_nl.md) | [Français](./README_fr.md) | Español | [Русский](./README_ru.md) | [العربية](./README_ar.md)
- [Referencia de la API](./API.md) · [Autenticación y medios](./API_AUTH_MEDIA.md) · [Arquitectura](./BACKEND.md) · [Licencia](../LICENSE)

## Acerca de

Mocca sirve una biblioteca de medios por HTTP. Es un único binario de Go —— sin CGO, sin Node, sin CDN.
`make full` compila la API junto con una consola de administración integrada; `make api` compila solo la API.

## Dos compilaciones

Un solo código fuente, dos binarios. La única diferencia es una etiqueta de compilación.

| | Completa | Solo API |
| --- | --- | --- |
| Compilación | `make full` (o `go build ./`) | `make api` (o `go build -tags noweb ./`) |
| Resultado | `bin/mocca` | `bin/mocca-api` |
| API REST + streaming | sí | sí (las rutas de la app son idénticas) |
| Comando `passwd` | sí | sí |
| Consola de administración `/admin/` | sí (integrada) | **no** —— `web/public/` no se compila |
| Endpoints de administración (almacenamiento, subida, edición, portada/captura, ajustes, usuarios) | sí | **no** —— todo el grupo no se registra (HTTP 404) |

## Inicio rápido

```bash
make full
./bin/mocca          # escucha en :8000 por defecto
```

1. Abre `http://localhost:8000/admin/` y registra la primera cuenta —— la primera pasa a ser administradora.
2. Añade un almacenamiento: ruta de montaje `/media`, controlador `local`, raíz apuntando a tu carpeta de medios.
3. Reproduce: `http://localhost:8000/d/media/movie.mp4?token=<tu-token>`.

¿Olvidaste la contraseña de administrador o la guardaste mal? La línea de comandos es el único camino de vuelta —— está incluida en **ambas** compilaciones:

```bash
./bin/mocca-api passwd -u admin            # la nueva contraseña se lee de stdin
./bin/mocca-api passwd -u admin -p 'nueva'
```

## Configuración

Mocca lee `.env` del directorio de trabajo (otra ubicación con `MOCCA_ENV_FILE=/path/to/xxx.env`) y
las variables de entorno del sistema con el prefijo `MOCCA_`. Prioridad: valores por defecto < entorno < `.env`.

| Clave `.env` | Variable de entorno | Valor por defecto | Significado |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | Dirección de escucha |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | Aquí vive la base de datos; las portadas y los índices están del lado del dispositivo, junto a los medios |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | Clave de firma de tokens —— cámbiala en producción |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | Contraseña del primer administrador, solo si no existe ninguno |

## Funciones

- **Almacenamientos** —— monta carpetas locales y recursos compartidos SMB (Samba); tantos puntos de montaje como quieras.
- **Streaming por rangos de bytes** —— `GET /d/<ruta>` admite `Range`, rangos múltiples, `HEAD` y peticiones condicionales, así que los reproductores pueden buscar y reanudar.
- **Metadatos de medios** —— título, duración, portada, descripción y varios autores por elemento; tipos vídeo / audio / imagen.
- **Generador de portadas** (solo compilación completa) —— la portada viene de una subida del admin o de un fotograma de ffmpeg en el instante elegido (`/api/fs/shot` para un archivo, `/api/fs/patch` para toda una carpeta); se redimensiona a PNG 400×300 y se guarda **del lado del dispositivo**, junto a la mediateca, en `<raíz-del-almacenamiento>/.mocca/ab/cd/<resto-del-sha1>.png`.
- **Consola de administración** (solo compilación completa) —— integrada y funcional sin conexión: almacenamientos, usuarios, metadatos, contraseñas de carpetas y subida por lotes con una barra de progreso por archivo.
- **Cuentas y permisos** —— administrador / usuario / invitado; cada usuario solo ve su propio `base_path`; 12 avatares predefinidos (subir imágenes no está soportado, por diseño).
- **Contraseñas de carpetas** —— se define una vez en la carpeta superior y todo el subárbol (streaming incluido) la exige; se guarda con bcrypt.
- **Comentarios, danmaku y favoritos** —— comentarios sin límite de longitud, danmaku de hasta 50 caracteres ordenados por momento de reproducción, favoritos sin duplicados por usuario.
- **Operación** —— comando de rescate `passwd`, un único binario estático y toda la configuración en `.env`.

## Licencia

[AGPL-3.0](../LICENSE).

## Colaboradores

- [@azhai](https://github.com/azhai) —— Mocca en sí
- Todas las personas que reportaron un problema o enviaron un parche

Las convenciones de la API de Mocca (hash estático de contraseña, envoltura de respuesta unificada) y parte de la documentación provienen del proyecto original [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist).
Gracias a [Xhofe](https://github.com/Xhofe) y a todos los colaboradores del proyecto original:

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
