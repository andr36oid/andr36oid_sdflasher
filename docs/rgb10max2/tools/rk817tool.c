/* rk817tool - read/write RK817 PMIC registers via /dev/i2c-0 (addr 0x20)
 * usage: rk817tool <reg>            read one register (hex, e.g. b4)
 *        rk817tool <reg> <value>    write then read back
 *        rk817tool dump <first> <last>  dump a range
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <linux/i2c-dev.h>
#include <linux/i2c.h>

#define I2C_DEV "/dev/i2c-0"
#define ADDR 0x20

static int fd;
static int xfer(uint8_t wr[], int wn, uint8_t rd[], int rn)
{
    struct i2c_msg msgs[2] = {
        { .addr = ADDR, .flags = 0,          .len = wn, .buf = wr },
        { .addr = ADDR, .flags = I2C_M_RD,   .len = rn, .buf = rd },
    };
    struct i2c_rdwr_ioctl_data d = { .msgs = msgs, .nmsgs = 2 };
    if (rn == 0) d.nmsgs = 1;
    return ioctl(fd, I2C_RDWR, &d);
}

static int rd_reg(uint8_t reg, uint8_t *val)
{
    uint8_t r;
    if (xfer(&reg, 1, &r, 1) < 0) return -1;
    *val = r;
    return 0;
}

static int wr_reg(uint8_t reg, uint8_t val)
{
    uint8_t w[2] = { reg, val };
    if (xfer(w, 2, NULL, 0) < 0) return -1;
    return 0;
}

int main(int argc, char **argv)
{
    if (argc < 2) { fprintf(stderr, "usage: %s <reg> [val] | dump <first> <last>\n", argv[0]); return 2; }
    fd = open(I2C_DEV, O_RDWR);
    if (fd < 0) { perror(I2C_DEV); return 1; }

    if (!strcmp(argv[1], "dump") && argc >= 4) {
        int a = strtol(argv[2], 0, 16), b = strtol(argv[3], 0, 16);
        for (; a <= b; a++) {
            uint8_t v;
            if (rd_reg(a, &v) < 0) { printf("%02x: ERR\n", a); continue; }
            printf("%02x %02x\n", a, v);
        }
        return 0;
    }
    int reg = strtol(argv[1], 0, 16);
    uint8_t v;
    if (argc >= 3) {
        int val = strtol(argv[2], 0, 16);
        if (wr_reg(reg, val) < 0) { perror("write"); return 1; }
        usleep(10000);
    }
    if (rd_reg(reg, &v) < 0) { perror("read"); return 1; }
    printf("%02x %02x\n", reg, v);
    return 0;
}
