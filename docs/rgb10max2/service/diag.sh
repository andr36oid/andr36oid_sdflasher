#!/system/bin/sh
# RGB10 Max 2 production v22
#  - internal Wi-Fi rail latch (RK817 PMIC latch, raw i2c poke at boot)
#  - radio tuning: power save off + MA regdomain (weak-link robustness, latency)
#  - network ADB + root command daemon (live servicing; cmd.in -> cmd.out)
D=/data/local/tmp
mkdir -p $D
echo "started $(date)" > $D/diag-status.txt
cp /sys/fs/pstore/* $D/ 2>/dev/null

RK=/vendor/etc/rk817tool
[ -x "$RK" ] || RK=$D/rk817tool
WF=/vendor/etc/wifitool
[ -x "$WF" ] || WF=$D/wifitool
DR=/sys/bus/platform/drivers/rk817-charger
devs() { ls /sys/bus/usb/devices 2>/dev/null | grep -c -- -; }

# preload the wifi driver so the dongle binds the moment it enumerates
insmod /vendor/lib/modules/rtl8xxxu.ko 2>>$D/insmod.err

# never let the hub bus idle (would drop the rail/hub)
for P in /sys/devices/platform/ff300000.usb /sys/bus/usb/devices/usb1 /sys/bus/usb/devices/usb2 /sys/bus/usb/devices/usb3; do
    [ -e "$P/power/control" ] && echo on > "$P/power/control" 2>>$D/pm.err
done

# ---- rail latch: only when the hub is not already powered ----
# (hub up => external 5V present or latch already set; never write then,
#  a write against external VBUS can trip the PMIC)
if [ "$(devs)" -lt 3 ] && [ -x "$RK" ]; then
    echo "rail: hub absent, poking latch" >> $D/diag-status.txt
    $RK b4 47 >> $D/rail.log 2>&1
    sleep 3
    if [ "$(devs)" -lt 3 ]; then
        echo "rail: poke didn't take, unbind/rebind ladder" >> $D/diag-status.txt
        echo rk817-charger > $DR/unbind 2>>$D/rail.log
        sleep 1
        $RK b4 47 >> $D/rail.log 2>&1
        sleep 3
        echo rk817-charger > $DR/bind 2>>$D/rail.log
        sleep 2
        [ "$(devs)" -lt 3 ] && { $RK b4 47 >> $D/rail.log 2>&1; sleep 2; }
    fi
    echo "rail: final devs=$(devs)" >> $D/diag-status.txt
else
    echo "rail: hub already up (devs=$(devs))" >> $D/diag-status.txt
fi

# ---- wait for boot ----
N=0
while [ "$(/system/bin/getprop sys.boot_completed)" != "1" ] && [ $N -lt 300 ]; do
    sleep 1; N=$((N+1))
done
echo "boot_completed after ${N}s" >> $D/diag-status.txt

# radio tuning once the interface exists (up to 60s)
M=0
while [ ! -e /sys/class/net/wlan0 ] && [ $M -lt 60 ]; do sleep 1; M=$((M+1)); done
if [ -e /sys/class/net/wlan0 ] && [ -x "$WF" ]; then
    $WF ps-off 2>>$D/radio.err
    $WF reg MA 2>>$D/radio.err
    echo "radio: ps-off + reg MA applied" >> $D/diag-status.txt
fi

setprop service.adb.tcp.port 5555
setprop ctl.restart adbd
echo "ADBTCP-ON" >> $D/diag-status.txt
chmod 666 $D/diag-status.txt $D/rail.log $D/*.err 2>/dev/null

# ---- MAIN LOOP: root command daemon + radio watchdog ----
I=0
while true; do
    I=$((I+1))
    if [ -f $D/cmd.in ]; then
        mv $D/cmd.in $D/cmd.running 2>/dev/null
        /system/bin/sh $D/cmd.running > $D/cmd.out 2>&1
        chmod 666 $D/cmd.out 2>/dev/null
        rm -f $D/cmd.running
    fi
    # keep power save off across driver/framework reloads
    if [ $((I % 30)) -eq 0 ] && [ -e /sys/class/net/wlan0 ] && [ -x "$WF" ]; then
        $WF ps-off > /dev/null 2>&1
    fi
    sleep 1
done
