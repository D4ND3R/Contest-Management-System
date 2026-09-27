# Glosario

Las palabras que usan el sistema y esta documentación, en orden alfabético.

- **Adjunto**: archivo de un problema que los concursantes descargan (un
  grader de ejemplo, un header, datos).
- **Apelación**: después del concurso, un concursante pide revisar un
  problema o un envío; los organizadores responden (aceptada o rechazada).
- **Aviso** (anuncio): mensaje de los organizadores para todos los
  concursantes de un concurso.
- **Banco de problemas**: todos los problemas del servidor, estén o no en
  un concurso.
- **Caso de prueba**: una entrada y la salida esperada. **Público**: el
  concursante ve su resultado uno por uno.
- **Checker**: decide si la salida de un programa es correcta. Los
  integrados comparan ignorando espacios, exactamente o como números reales
  con tolerancia; uno **propio** es un programa (protocolo CMS o testlib).
- **Clarificación**: una pregunta de un concursante con su respuesta; si la
  respuesta es pública, la ven todos.
- **Congelar la clasificación**: al final del concurso la clasificación
  pública deja de cambiar (los envíos posteriores se muestran como `?`)
  hasta que los organizadores la descongelan.
- **Cortocircuito**: dejar de evaluar una subtarea en cuanto un caso da 0.
- **Dataset**: la configuración de evaluación de un problema (tipo,
  límites, casos, checker, managers, puntuación). El **dataset en vivo** es
  el que puntúa los envíos; otros sirven para probar cambios.
- **Despachador** (*dispatcher*): el servicio que reparte el trabajo a los
  jueces y guarda los resultados.
- **Envío**: el código que un concursante manda para un problema.
  **Oficial** si cuenta; **no oficial** en práctica o si fue invalidado.
- **Espera entre envíos**: tiempo mínimo entre dos envíos de un concursante
  (el botón Enviar hace la cuenta regresiva).
- **Generador**: programa que escribe entradas de casos de prueba a partir
  de parámetros.
- **Grader**: código de los organizadores que se compila junto con el del
  concursante (problemas donde se implementa una función).
- **Interactor**: programa que dialoga con el del concursante en los
  problemas interactivos y decide el veredicto.
- **Juez** (*worker*): el servicio que compila y ejecuta los programas en un
  sandbox (`isolate`). Puede haber varios, en varias máquinas.
- **Manager**: cualquier archivo auxiliar del dataset (checker, grader,
  stub, header, interactor, manager de comunicación).
- **Modo de puntuación**: cómo se combinan los envíos de un concursante en
  un problema: mejor por subtarea (IOI), mejor envío, el último...
- **Participación**: la inscripción de un usuario en un concurso (con sus
  ajustes: tiempo extra, IP, oculto...).
- **Probador de problemas**: envía una solución como organizador; se evalúa
  en todos los datasets y nunca cuenta. Esa ejecución es una **solución de
  referencia**.
- **Sandbox**: el entorno aislado donde corren los programas, con límites de
  tiempo, memoria y procesos, y un filtro de llamadas al sistema.
- **Sede**: un lugar con su propia hora de inicio (concursos en varias
  ciudades).
- **Subtarea**: grupo de casos con puntos propios.
- **Token**: permite ver el resultado completo de un envío durante un
  concurso que oculta los resultados.
- **Veredicto**: el resultado de un envío o un caso: AC, PA, WA, TLE, MLE,
  RE, OLE, SV, CE, SK (ver la [guía del concursante](guia-del-concursante.md#veredictos)).
- **Zona horaria del servidor**: la zona en la que todos los sitios muestran
  las horas (**Servidor** en la administración).
