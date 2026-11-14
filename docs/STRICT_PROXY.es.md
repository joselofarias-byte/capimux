# strictProxy: comportamiento y política de destinos locales

`strictProxy` se puede activar en la conexión (campo de nivel superior), en `providerSpecificData.strictProxy` o en el pool de proxy. Alcanza con que uno de los tres esté activo para que el tráfico de esa conexión **nunca salga directo**: si el proxy obligatorio no está disponible, la operación falla.

## Pedidos de chat y media (`getClientForConnection`)

- Pool inexistente, inactivo, sin URL, con URL inválida o de tipo desconocido: el cliente devuelve un error y no hace ninguna conexión.
- Relays edge (Vercel, Cloudflare, Deno): con `strictProxy`, el cliente solo puede hablar con los hosts del relay. Cualquier otro host se rechaza. Esto protege también a los callers que, ante un error de configuración, intentan usar la URL por defecto del proveedor.

## Relays edge (`getProviderConfig`)

- Sin `strictProxy`, no cambia nada: un upstream que coincide con `noProxy`, o un upstream loopback, saltea el relay y va directo.
- Con `strictProxy`, ni una coincidencia con `noProxy` ni un upstream loopback pueden saltear el relay: se devuelve un error y no hay tráfico directo.

## Pruebas de conexión del dashboard (`probeHTTPClient`)

- Con `strictProxy`, la prueba falla con un mensaje `strict proxy: ...` y sin contactar al proveedor cuando: el pool no existe o está inactivo, no hay URL, la URL es inválida, el tipo es desconocido, no hay ningún proxy configurado o el pool es un relay edge (la prueba no sabe enrutar por relay).
- Si el proxy obligatorio falla durante la prueba, no se reintenta con el cliente directo. Sin `strictProxy`, se mantiene el reintento directo de siempre.

## Destinos locales y privados (SSRF)

Este cambio no agrega ningún permiso implícito para destinos locales o privados. El único camino explícito hacia destinos locales sigue siendo un proveedor `LocalOnly` (por ejemplo, llama.cpp), que exige una URL loopback y nunca usa relay. Un upstream loopback ya no puede usarse para esquivar una política `strictProxy`.
