# Sistema de Consulta de Clima por CEP com Tracing (OTEL + Zipkin)

Este projeto consiste em dois serviços Go (Serviço A e Serviço B) que trabalham juntos para fornecer informações de clima baseadas em um CEP, com tracing distribuído implementado usando OpenTelemetry (OTEL) e Zipkin.

- **Serviço A:** Recebe um CEP via POST, valida-o e encaminha a requisição para o Serviço B.
- **Serviço B:** Recebe o CEP do Serviço A, busca a localização (ViaCEP) e o clima (WeatherAPI), e retorna a cidade e as temperaturas (C, F, K).
- **Zipkin:** Coleta e visualiza os traces distribuídos gerados pelos serviços.

## Estrutura do Projeto

```
/
├── service-a/
│   ├── main.go
│   ├── go.mod
│   ├── go.sum
│   └── Dockerfile
├── cep-weather-api/  (Serviço B)
│   ├── main.go
│   ├── go.mod
│   ├── go.sum
│   └── Dockerfile
├── docker-compose.yml
└── README.md         (Este arquivo)
```

## Pré-requisitos

- Docker e Docker Compose instalados.
- Uma chave de API válida da WeatherAPI (https://www.weatherapi.com/).

## Como Executar Localmente com Docker Compose

1.  **Clonar/Baixar o Projeto:** Certifique-se de ter todos os arquivos e diretórios (`service-a`, `cep-weather-api`, `docker-compose.yml`, `README.md`) na mesma pasta raiz.

2.  **Configurar a Chave da WeatherAPI:**
    Crie um arquivo chamado `.env` na mesma pasta que o `docker-compose.yml` e adicione sua chave da WeatherAPI:
    ```.env
    WEATHER_API_KEY=SUA_CHAVE_AQUI
    ```
    Substitua `SUA_CHAVE_AQUI` pela sua chave real.

3.  **Construir e Iniciar os Containers:**
    No terminal, navegue até a pasta raiz do projeto (onde está o `docker-compose.yml`) e execute:
    ```bash
    docker-compose up --build
    ```
    Este comando irá construir as imagens Docker para o Serviço A e Serviço B (se ainda não existirem ou se o código mudou) e iniciar os três containers (Service A, Service B, Zipkin).

4.  **Fazer uma Requisição:**
    Abra outro terminal ou use uma ferramenta como Postman/Insomnia para enviar uma requisição POST para o Serviço A (que está exposto na porta 8080):
    ```bash
    curl -X POST http://localhost:8080/ -H "Content-Type: application/json" -d '{"cep": "01001000"}'
    ```
    Substitua `01001000` pelo CEP desejado.

    **Exemplos de Respostas:**
    - **Sucesso (CEP: 01001000):**
      ```json
      {"city":"São Paulo","temp_C":21.2,"temp_F":70.16,"temp_K":294.35}
      ```
      (Status Code: 200 OK)
    - **CEP Inválido (Formato):**
      ```bash
      curl -X POST http://localhost:8080/ -H "Content-Type: application/json" -d '{"cep": "123"}'
      ```
      Resposta: `invalid zipcode` (Status Code: 422 Unprocessable Entity)
    - **CEP Não Encontrado (Formato válido, mas inexistente):**
      ```bash
      curl -X POST http://localhost:8080/ -H "Content-Type: application/json" -d '{"cep": "99999999"}'
      ```
      Resposta: `can not find zipcode` (Status Code: 404 Not Found)

5.  **Visualizar Traces no Zipkin:**
    Abra seu navegador e acesse a interface do Zipkin:
    [http://localhost:9411/zipkin/](http://localhost:9411/zipkin/)

    Você deverá ver os traces das requisições que você fez. Clique em "Run Query" para ver os traces mais recentes. Explore um trace para ver os spans individuais do Serviço A, Serviço B, e as chamadas para as APIs externas (ViaCEP e WeatherAPI), incluindo seus tempos de execução.

6.  **Parar os Containers:**
    Quando terminar, pressione `Ctrl + C` no terminal onde o `docker-compose up` está rodando. Para remover os containers, você pode usar:
    ```bash
    docker-compose down
    ```

## Variáveis de Ambiente Configuráveis (via `docker-compose.yml` ou `.env`)

- `WEATHER_API_KEY`: (Obrigatório para Serviço B) Sua chave da WeatherAPI.
- `PORT`: Porta em que cada serviço escutará (Padrão: 8080 para A, 8081 para B).
- `SERVICE_B_URL`: URL interna que o Serviço A usa para chamar o Serviço B (Padrão: `http://service-b:8081`).
- `OTEL_EXPORTER_ZIPKIN_ENDPOINT`: URL interna para onde os serviços enviam os traces (Padrão: `http://zipkin:9411/api/v2/spans`).

