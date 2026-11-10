# Comparador multimodelo de Capimux — JolufaSoft

El comparador permite elegir de **2 a 10 modelos por tarea**, sin imponer nombres de modelos ni proveedores. Se mantiene la selección de un único modelo para compatibilidad y para ejecución manual. Configurá conexiones autorizadas antes de usarlas.

## Política y privacidad

- `CAPIMUX_MULTIAGENT_ALLOWED_MODELS`: identificadores exactos `proveedor/modelo`, separados por coma. Es una lista del operador, no prueba de disponibilidad, saldo ni gratuidad.
- `CAPIMUX_MULTIAGENT_ENABLE_EXTERNAL=1`: habilita envíos. Sin esa variable solo se permite simulación.
- Las rutas del comparador exigen sesión administrativa o token CLI local incluso con `requireLogin` desactivado. Las claves de inferencia no habilitan estas operaciones.
- Los perfiles son **locales al navegador**, uno por tarea: guardan estrategia y modelos ordenados, nunca prompt, respuesta ni credenciales. No son sincronización entre dispositivos. Al cargarlos se eliminan modelos fuera de la política actual. Se pueden borrar desde la misma vista.
- El comparador no agrega logs de contenido. Reutiliza el registro de uso del router; revisá la configuración de diagnósticos antes de habilitarlos. `CAPIMUX_REQUEST_DETAILS_RETENTION_DAYS` controla los detalles locales entre 1 y 365 días (predeterminado: 7). Esto no es una política universal de borrado de todas las tablas de uso ni de backups.

## Tareas y estrategias

Tareas: programación, revisión, análisis, conversación, traducción y razonamiento. La tarea **clasifica el perfil, no altera el prompt ni lo envía a modelos adicionales**.

| Estrategia | Comportamiento |
|---|---|
| En paralelo (predeterminada) | Ejecuta todos los modelos elegidos y conserva el orden de resultados. Un error no borra otras respuestas. |
| Prioridad / respaldo | Recorre el orden elegido hasta el primer éxito. Solo avanza tras HTTP 429 o 5xx explícito. Cada modelo aparece una vez. |
| Rotación | Elige uno por lote usando un contador del handler en memoria; se reinicia al reiniciar el proceso y comparte turno entre listas/tareas. No es balanceo distribuido. |
| Manual | Ejecuta únicamente el primer modelo de la selección ordenada. |

Prioridad y respaldo son dos etiquetas para la misma política secuencial conservadora. No se avanza por timeout, desconexión, error ambiguo de transporte, respuesta vacía/inválida o respuesta excesiva: el proveedor podría haber procesado la llamada. No existe garantía de ejecución exactamente una vez; incluso un 5xx puede representar trabajo ya realizado por un proveedor. El router de chat subyacente conserva sus propias políticas de cuentas y combos: estas reglas describen únicamente los saltos añadidos por el comparador.

## Límites y API

Diez modelos por lote, tres llamadas simultáneas por defecto y dos lotes activos en el proceso. Cada llamada dispone de 60 segundos por defecto (hasta 120 configurables), como máximo 4096 tokens de salida y un buffer de respuesta de 1 MiB. No es un presupuesto monetario.

`POST /api/multiagent/run` agrega campos opcionales `task` (`programming`, `review`, `analysis`, `conversation`, `translation`, `reasoning`) y `strategy` (`parallel`, `priority`, `fallback`, `round-robin`, `manual`). Omitirlos conserva el comportamiento anterior. Valores desconocidos responden 400. La política anuncia `max_models: 10`; no hay migración de base de datos.

Cancelar o salir de la vista aborta la solicitud y propaga cancelación al proveedor. La cancelación no revierte trabajo ya procesado. El comparador usa Chat Completions **sin streaming** y no ejecuta herramientas. SSE, Responses y herramientas dependen de la ruta/proveedor, no de esta vista.

## Verificación

Las pruebas unitarias cubren límites, listas autorizadas, orden, estrategias, cancelación, errores y no exposición del cuerpo de error del proveedor. La integración con el router real usa base temporal y upstream HTTP simulado. La simulación verifica selección, **no conectividad**. Para AGY/OpenCode consultá [AGY-OPENCODE.es.md](AGY-OPENCODE.es.md).
