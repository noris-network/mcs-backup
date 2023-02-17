
mermaid test page

```mermaid

graph TB

    subgraph  Application Pod
        direction LR
        app[Application Container]
        mcsbackup[Backup Sidecar]
    end

    volume[(Volume)]
    app-- mount -->volume
    mcsbackup-- mount -->volume

    s3[(S3 Storage)]
    mcsbackup -- backup  --> s3
    s3  -- restore --> mcsbackup

    influxdb[(Influxdb\nstore events)]
    mcsbackup -- post events --> influxdb

    prometheus[(Prometheus)]
    prometheus -->|fetch metrics|mcsbackup

```