#ifndef SM_SHIM_H
#define SM_SHIM_H
#include <stdint.h>

/* A flat, fixed-size view of Level Zero Sysman, so the Go side never touches
 * the API's nested structures. Every counter is cumulative, exactly as the
 * driver reports it; turning them into rates is the caller's job. */

#define SM_MAX 16

typedef struct {
	char bdf[24]; char name[256];
	unsigned vendor_id, device_id;
	unsigned char uuid[16];
	char driver_version[64];
	int ecc_state;      /* -1 unsupported, else zes_device_ecc_state_t */
	double freq_max_mhz; /* GPU frequency domain ceiling, 0 if unknown */
	char gfx_firmware[128];
	char oprom_firmware[128];
	int integrated;
	int ecc_available;  /* -1 unknown, else 0/1 */
	uint64_t bar_bytes; /* largest memory BAR (the CPU's VRAM window), 0 unknown */
	int mem_type;       /* -1 unknown, else zes_mem_type_t */
	int mem_bus_width;  /* bits, -1 unknown */
	int mem_channels;   /* -1 unknown */
} sm_info;
typedef struct { int type; double c; } sm_temp;
typedef struct { int domain; uint64_t energy_uj; uint64_t ts_us; } sm_power;
typedef struct { int type; double actual; double request; } sm_freq;

typedef struct {
	int have_mem;  uint64_t mem_size, mem_free;
	int have_bw;   uint64_t bw_read, bw_write, bw_max, bw_ts;
	int ntemp;     sm_temp temp[SM_MAX];
	int npower;    sm_power power[SM_MAX];
	int nfreq;     sm_freq freq[SM_MAX];
	int have_pci;  uint64_t pci_rx, pci_tx, pci_ts;
	int have_state; unsigned reset_reasons; int repaired; /* zes_reset_reason_flags_t, zes_repair_status_t */
} sm_sample;

/* Returns the device count, or a negative code: -1000 if libze_loader cannot
 * be loaded, -(ze_result_t) if Sysman cannot be initialised. */
int sm_init(void);
int sm_device_count(void);
int sm_device_info(int i, sm_info *out);
/* 0 on success; nonzero if not a single reading could be taken. */
int sm_device_sample(int i, sm_sample *out);

#endif
