#include <dlfcn.h>
#include <stdio.h>
#include <string.h>
#include <level_zero/ze_api.h>
#include <level_zero/zes_api.h>
#include "shim.h"

/* Level Zero is loaded with dlopen so a binary still starts on a machine that
 * does not have it. __typeof__ takes each prototype from the header, so the
 * pointers cannot drift from the real signatures, and nothing here creates a
 * link-time reference to the library. */
#define SM_FUNCS(X) \
	X(zesInit) X(zesDriverGet) X(zesDeviceGet) X(zesDeviceGetProperties) \
	X(zesDevicePciGetProperties) X(zesDevicePciGetStats) \
	X(zesDeviceEnumMemoryModules) X(zesMemoryGetState) X(zesMemoryGetBandwidth) \
	X(zesDeviceEnumTemperatureSensors) X(zesTemperatureGetProperties) X(zesTemperatureGetState) \
	X(zesDeviceEnumPowerDomains) X(zesPowerGetProperties) X(zesPowerGetEnergyCounter) \
	X(zesDeviceEnumFrequencyDomains) X(zesFrequencyGetProperties) X(zesFrequencyGetState) \
	X(zesDeviceGetEccState) X(zesDeviceEnumFirmwares) X(zesFirmwareGetProperties)

#define SM_DECL(name) static __typeof__(&name) f_##name;
SM_FUNCS(SM_DECL)

typedef struct {
	zes_device_handle_t dev;
	sm_info info;
	int nmem;   zes_mem_handle_t mem[SM_MAX];
	int ntemp;  zes_temp_handle_t temp[SM_MAX]; int temp_type[SM_MAX];
	int npower; zes_pwr_handle_t power[SM_MAX]; int power_domain[SM_MAX];
	int nfreq;  zes_freq_handle_t freq[SM_MAX]; int freq_type[SM_MAX];
} sm_dev;

static sm_dev devs[SM_MAX];
static int ndevs = -1; /* -1: not initialised */

int sm_device_count(void) { return ndevs < 0 ? 0 : ndevs; }

int sm_init(void) {
	if (ndevs >= 0) return ndevs;
	void *h = dlopen("libze_loader.so.1", RTLD_NOW | RTLD_LOCAL);
	if (!h) return -1000;
#define SM_LOAD(name) if (!(f_##name = (__typeof__(&name))dlsym(h, #name))) return -1001;
	SM_FUNCS(SM_LOAD)

	ze_result_t r = f_zesInit(0);
	if (r) return -(int)r;
	uint32_t nd = 0;
	if ((r = f_zesDriverGet(&nd, NULL)) || nd == 0) return r ? -(int)r : 0;
	zes_driver_handle_t drivers[SM_MAX];
	if (nd > SM_MAX) nd = SM_MAX;
	if ((r = f_zesDriverGet(&nd, drivers))) return -(int)r;

	int count = 0;
	for (uint32_t i = 0; i < nd && count < SM_MAX; i++) {
		uint32_t n = 0;
		if (f_zesDeviceGet(drivers[i], &n, NULL) || n == 0) continue;
		zes_device_handle_t hs[SM_MAX];
		if (n > SM_MAX) n = SM_MAX;
		if (f_zesDeviceGet(drivers[i], &n, hs)) continue;
		for (uint32_t j = 0; j < n && count < SM_MAX; j++) {
			sm_dev *d = &devs[count];
			memset(d, 0, sizeof *d);
			d->dev = hs[j];

			zes_pci_properties_t pp = {.stype = ZES_STRUCTURE_TYPE_PCI_PROPERTIES};
			zes_device_properties_t props = {.stype = ZES_STRUCTURE_TYPE_DEVICE_PROPERTIES};
			if (f_zesDevicePciGetProperties(d->dev, &pp)) continue;
			snprintf(d->info.bdf, sizeof d->info.bdf, "%04x:%02x:%02x.%x", pp.address.domain,
			         pp.address.bus, pp.address.device, pp.address.function);
			if (!f_zesDeviceGetProperties(d->dev, &props)) {
				snprintf(d->info.name, sizeof d->info.name, "%s", props.core.name);
				snprintf(d->info.driver_version, sizeof d->info.driver_version, "%s", props.driverVersion);
				d->info.vendor_id = props.core.vendorId;
				d->info.device_id = props.core.deviceId;
				memcpy(d->info.uuid, props.core.uuid.id, 16);
				d->info.integrated = !!(props.core.flags & ZE_DEVICE_PROPERTY_FLAG_INTEGRATED);
			}
			{
				zes_device_ecc_properties_t ep = {.stype = ZES_STRUCTURE_TYPE_DEVICE_ECC_PROPERTIES};
				d->info.ecc_state = f_zesDeviceGetEccState(d->dev, &ep) ? -1 : (int)ep.currentState;
			}
			{
				uint32_t nf = SM_MAX; zes_firmware_handle_t fw[SM_MAX];
				if (!f_zesDeviceEnumFirmwares(d->dev, &nf, fw)) {
					for (uint32_t k = 0; k < nf; k++) {
						zes_firmware_properties_t fp = {.stype = ZES_STRUCTURE_TYPE_FIRMWARE_PROPERTIES};
						if (!f_zesFirmwareGetProperties(fw[k], &fp) && strcmp(fp.name, "GFX") == 0)
							snprintf(d->info.gfx_firmware, sizeof d->info.gfx_firmware, "%s", fp.version);
					}
				}
			}

			uint32_t c = SM_MAX;
			if (!f_zesDeviceEnumMemoryModules(d->dev, &c, d->mem)) d->nmem = c;

			c = SM_MAX;
			if (!f_zesDeviceEnumTemperatureSensors(d->dev, &c, d->temp)) {
				d->ntemp = c;
				for (int k = 0; k < d->ntemp; k++) {
					zes_temp_properties_t tp = {.stype = ZES_STRUCTURE_TYPE_TEMP_PROPERTIES};
					d->temp_type[k] = f_zesTemperatureGetProperties(d->temp[k], &tp) ? -1 : (int)tp.type;
				}
			}
			c = SM_MAX;
			if (!f_zesDeviceEnumPowerDomains(d->dev, &c, d->power)) {
				d->npower = c;
				for (int k = 0; k < d->npower; k++) {
					zes_power_ext_properties_t ext = {.stype = ZES_STRUCTURE_TYPE_POWER_EXT_PROPERTIES};
					zes_power_properties_t pr = {.stype = ZES_STRUCTURE_TYPE_POWER_PROPERTIES, .pNext = &ext};
					d->power_domain[k] = f_zesPowerGetProperties(d->power[k], &pr) ? 0 : (int)ext.domain;
				}
			}
			c = SM_MAX;
			if (!f_zesDeviceEnumFrequencyDomains(d->dev, &c, d->freq)) {
				d->nfreq = c;
				for (int k = 0; k < d->nfreq; k++) {
					zes_freq_properties_t fp = {.stype = ZES_STRUCTURE_TYPE_FREQ_PROPERTIES};
					d->freq_type[k] = f_zesFrequencyGetProperties(d->freq[k], &fp) ? -1 : (int)fp.type;
					if (d->freq_type[k] == ZES_FREQ_DOMAIN_GPU) d->info.freq_max_mhz = fp.max;
				}
			}
			count++;
		}
	}
	ndevs = count;
	return count;
}

int sm_device_info(int i, sm_info *out) {
	if (i < 0 || i >= ndevs) return -1;
	*out = devs[i].info;
	return 0;
}

int sm_device_sample(int i, sm_sample *out) {
	if (i < 0 || i >= ndevs) return -1;
	sm_dev *d = &devs[i];
	int got = 0;
	memset(out, 0, sizeof *out);

	for (int k = 0; k < d->nmem; k++) {
		zes_mem_state_t st = {.stype = ZES_STRUCTURE_TYPE_MEM_STATE};
		if (!f_zesMemoryGetState(d->mem[k], &st)) {
			out->have_mem = 1; out->mem_size += st.size; out->mem_free += st.free; got = 1;
		}
		zes_mem_bandwidth_t bw;
		if (!f_zesMemoryGetBandwidth(d->mem[k], &bw)) {
			out->have_bw = 1; out->bw_read += bw.readCounter; out->bw_write += bw.writeCounter;
			out->bw_max += bw.maxBandwidth; out->bw_ts = bw.timestamp; got = 1;
		}
	}
	for (int k = 0; k < d->ntemp && out->ntemp < SM_MAX; k++) {
		double c;
		if (d->temp_type[k] >= 0 && !f_zesTemperatureGetState(d->temp[k], &c)) {
			out->temp[out->ntemp].type = d->temp_type[k];
			out->temp[out->ntemp++].c = c; got = 1;
		}
	}
	for (int k = 0; k < d->npower && out->npower < SM_MAX; k++) {
		zes_power_energy_counter_t e;
		if (!f_zesPowerGetEnergyCounter(d->power[k], &e)) {
			out->power[out->npower].domain = d->power_domain[k];
			out->power[out->npower].energy_uj = e.energy;
			out->power[out->npower++].ts_us = e.timestamp; got = 1;
		}
	}
	for (int k = 0; k < d->nfreq && out->nfreq < SM_MAX; k++) {
		zes_freq_state_t s = {.stype = ZES_STRUCTURE_TYPE_FREQ_STATE};
		if (d->freq_type[k] >= 0 && !f_zesFrequencyGetState(d->freq[k], &s)) {
			out->freq[out->nfreq].type = d->freq_type[k];
			out->freq[out->nfreq].actual = s.actual;
			out->freq[out->nfreq++].request = s.request; got = 1;
		}
	}
	zes_pci_stats_t ps;
	if (!f_zesDevicePciGetStats(d->dev, &ps)) {
		out->have_pci = 1; out->pci_rx = ps.rxCounter; out->pci_tx = ps.txCounter;
		out->pci_ts = ps.timestamp; got = 1;
	}
	return got ? 0 : 1;
}
