package metrics

import (
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/utils"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

const (
	// SETTINGS FOR DATA ANALYSIS
	MAX_SPEED_SAMPLES   = 100 // KEEP LAST 100 SAMPLES FOR SPEED CALC
	MAX_TIME_POINTS     = 180 // 15 MINUTES OF DATA / 10 SECOND INTERVALS
	COLLECTION_INTERVAL = 10  // EVERY 10 SECONDS
)

const (
	B  = 1
	KB = 1024 * B
	MB = 1024 * KB
	GB = 1024 * MB
)

type MetricsService struct {
	mu           sync.RWMutex
	log          *logging.LogService
	data         models.MetricsData
	speedSamples struct {
		upload   []float64
		download []float64
	}
	timeseriesData []models.TimeSeriesPoint
	dataDir        string
	accessLogs     []models.AccessLogEntry
	maxLogs        int // Maximum number of logs to keep
	logMutex       sync.RWMutex
}

func NewMetricsService(log *logging.LogService, dataDir string) *MetricsService {
	ms := &MetricsService{
		log:            log,
		dataDir:        dataDir,
		timeseriesData: make([]models.TimeSeriesPoint, 0, MAX_TIME_POINTS),
		speedSamples: struct {
			upload   []float64
			download []float64
		}{
			upload:   make([]float64, 0, MAX_SPEED_SAMPLES),
			download: make([]float64, 0, MAX_SPEED_SAMPLES),
		},
		accessLogs: make([]models.AccessLogEntry, 0),
		maxLogs:    1000, // KEEP LAST 1K ENTRIES
		logMutex:   sync.RWMutex{},
	}
	go ms.collectMetrics()
	return ms
}

func (ms *MetricsService) collectMetrics() {
	ticker := time.NewTicker(COLLECTION_INTERVAL * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ms.mu.Lock()

		uploadSpeed := ms.calculateAverageSpeed(ms.speedSamples.upload) / MB
		downloadSpeed := ms.calculateAverageSpeed(ms.speedSamples.download) / MB

		point := models.TimeSeriesPoint{
			Timestamp:     time.Now(),
			UploadSpeed:   uploadSpeed,
			DownloadSpeed: downloadSpeed,
			ActiveUploads: ms.data.BlobUploads.InProgress,
		}

		ms.timeseriesData = append(ms.timeseriesData, point)
		if len(ms.timeseriesData) > MAX_TIME_POINTS {
			ms.timeseriesData = ms.timeseriesData[1:]
		}

		ms.updateSystemMetrics()
		ms.mu.Unlock()
	}
}

func (ms *MetricsService) calculateAverageSpeed(speeds []float64) float64 {
	if len(speeds) == 0 {
		return 0
	}
	var sum float64
	for _, speed := range speeds {
		sum += speed
	}
	return math.Round((sum/float64(len(speeds)))*100) / 100
}

func (ms *MetricsService) addSpeedSample(speeds *[]float64, newSpeed float64) {
	*speeds = append(*speeds, newSpeed)
	if len(*speeds) > MAX_SPEED_SAMPLES {
		*speeds = (*speeds)[1:]
	}
}

func (ms *MetricsService) updateSystemMetrics() {
	if vm, err := mem.VirtualMemory(); err != nil {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		ms.data.Performance.MemoryUsage = int64(mem.Alloc)
		ms.data.Performance.MemoryTotal = int64(mem.Sys)
	} else {
		ms.data.Performance.MemoryUsage = int64(vm.Used)
		ms.data.Performance.MemoryTotal = int64(vm.Total)
	}

	diskInfo := utils.GetDiskInfo(ms.dataDir)
	ms.data.Performance.DiskTotal = diskInfo.DiskTotal
	ms.data.Performance.DiskUsage = diskInfo.DiskTotal - diskInfo.DiskAvailable
	ms.data.Performance.AvgUploadSpeed = ms.calculateAverageSpeed(ms.speedSamples.upload)
	ms.data.Performance.AvgDownloadSpeed = ms.calculateAverageSpeed(ms.speedSamples.download)

	cpuUsage, err := cpu.Percent(time.Second, false)
	if err != nil || len(cpuUsage) == 0 {
		ms.data.Performance.CpuUsage = 0
	} else {
		ms.data.Performance.CpuUsage = ms.calculateAverageSpeed(cpuUsage)
	}
}

func (ms *MetricsService) TrackUploadStart() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data.BlobUploads.InProgress++
}

func (ms *MetricsService) TrackUploadComplete(bytes int64, duration time.Duration) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.data.BlobUploads.Total++
	ms.data.BlobUploads.InProgress--
	ms.data.BlobUploads.BytesProcessed += bytes

	if duration.Seconds() > 0 {
		bytesPerSec := float64(bytes) / duration.Seconds()
		ms.addSpeedSample(&ms.speedSamples.upload, bytesPerSec)
	}

	totalUploads := float64(ms.data.BlobUploads.Total)
	if totalUploads > 0 {
		ms.data.BlobUploads.AvgDuration = (ms.data.BlobUploads.AvgDuration*(totalUploads-1) +
			float64(duration.Milliseconds())) / totalUploads
	}
}

func (ms *MetricsService) TrackUploadFailed() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data.BlobUploads.Total++
	ms.data.BlobUploads.Failed++
	ms.data.BlobUploads.InProgress--
}

func (ms *MetricsService) TrackDownloadStart() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data.BlobDownloads.InProgress++
}

func (ms *MetricsService) TrackDownloadComplete(bytes int64, duration time.Duration) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.data.BlobDownloads.Total++
	ms.data.BlobDownloads.InProgress--
	ms.data.BlobDownloads.BytesProcessed += bytes

	if duration.Seconds() > 0 {
		bytesPerSec := float64(bytes) / duration.Seconds()
		ms.addSpeedSample(&ms.speedSamples.download, bytesPerSec)
	}

	totalDownloads := float64(ms.data.BlobDownloads.Total)
	if totalDownloads > 0 {
		ms.data.BlobDownloads.AvgDuration = (ms.data.BlobDownloads.AvgDuration*(totalDownloads-1) +
			float64(duration.Milliseconds())) / totalDownloads
	}
}

func (ms *MetricsService) TrackDownloadFailed() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data.BlobUploads.Total++
	ms.data.BlobDownloads.Failed++
	ms.data.BlobDownloads.InProgress--
}

func (ms *MetricsService) GetMetrics() models.MetricsData {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	dataCopy := models.MetricsData{
		BlobUploads:    ms.data.BlobUploads,
		BlobDownloads:  ms.data.BlobDownloads,
		Performance:    ms.data.Performance,
		TimeseriesData: make([]models.TimeSeriesPoint, len(ms.timeseriesData)),
		AccessLogs:     ms.GetAccessLogs(),
	}
	copy(dataCopy.TimeseriesData, ms.timeseriesData)
	return dataCopy
}

func (ms *MetricsService) AddAccessLog(entry models.AccessLogEntry) {
	ms.logMutex.Lock()
	defer ms.logMutex.Unlock()

	// ADD NEW ENTRIES TO THE BEGINNING
	ms.accessLogs = append([]models.AccessLogEntry{entry}, ms.accessLogs...)

	// TRIM EXCESS
	if len(ms.accessLogs) > ms.maxLogs {
		ms.accessLogs = ms.accessLogs[:ms.maxLogs]
	}
}

func (ms *MetricsService) GetAccessLogs() []models.AccessLogEntry {
	ms.logMutex.RLock()
	defer ms.logMutex.RUnlock()

	// GET COPY ONLY, WE ARENT MODIFYING THE SOURCE ITEM
	logs := make([]models.AccessLogEntry, len(ms.accessLogs))
	copy(logs, ms.accessLogs)
	return logs
}
