#!/system/bin/sh
# RGB10 Max 2 production v27 - stock topology + latch + panel cycle
#  - otg-port disabled (stock): the charger driver never learns about OTG, so
#    charging and Wi-Fi work together, exactly like the stock firmware
#  - boot service arms the PMIC OTG rail latch (raw i2c) and keeps it armed
#  - watchdog re-arms the latch whenever the hub loses power
#  - panel fix: sleep/wake cycle after bootanim stops (SF/HWC never enables
#    the CRTC for the Android framebuffer; the cycle triggers the modeset)
#  - radio tuning (power save off, regdomain), adb over tcp, root daemon
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

insmod /vendor/lib/modules/rtl8xxxu.ko 2>>$D/insmod.err

for P in /sys/devices/platform/ff300000.usb /sys/bus/usb/devices/usb1 /sys/bus/usb/devices/usb2 /sys/bus/usb/devices/usb3; do
    [ -e "$P/power/control" ] && echo on > "$P/power/control" 2>>$D/pm.err
done

# ---- arm the Wi-Fi rail latch (always; charging is independent now) ----
if [ -x "$RK" ]; then
    if [ "$(devs)" -ge 3 ]; then
        echo "rail: hub already up (devs=$(devs))" >> $D/diag-status.txt
    else
        echo "rail: arming latch" >> $D/diag-status.txt
        $RK b4 47 >> $D/rail.log 2>&1
        sleep 3
        if [ "$(devs)" -lt 3 ]; then
            echo "rail: retry with charger unbind/rebind" >> $D/diag-status.txt
            echo rk817-charger > $DR/unbind 2>>$D/rail.log
            sleep 1
            $RK b4 47 >> $D/rail.log 2>&1
            sleep 3
            echo rk817-charger > $DR/bind 2>>$D/rail.log
            sleep 2
            [ "$(devs)" -lt 3 ] && { $RK b4 47 >> $D/rail.log 2>&1; sleep 2; }
        fi
        echo "rail: final devs=$(devs)" >> $D/diag-status.txt
    fi
fi

# ---- wait for boot ----
N=0
while [ "$(/system/bin/getprop sys.boot_completed)" != "1" ] && [ $N -lt 300 ]; do
    sleep 1; N=$((N+1))
done
echo "boot_completed after ${N}s" >> $D/diag-status.txt

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

# ---- PANEL FIX: force the display DPMS cycle ----
# The kernel draws the boot logo, but when SurfaceFlinger/HWC take over the
# DRM master the CRTC is never (re)enabled for the Android framebuffer, so
# the panel stays black while Android believes the screen is on. A
# sleep/wake cycle forces DisplayPowerController to disable+re-enable the
# display, which triggers the CRTC modeset and the picture appears.
# This replicates exactly what the user had to do by hand (power button).
# NOTE: input injection needs system_server, and this code runs only after
# sys.boot_completed=1. The bootanim wait accepts "stopped" AND empty
# (nobootanimation builds never set the service running).
M=0
while [ "$(getprop init.svc.bootanim)" = "running" ] && [ $M -lt 120 ]; do
    sleep 1; M=$((M+1))
done
echo "bootanim state reached after ${M}s, cycling panel" >> $D/diag-status.txt
input keyevent KEYCODE_POWER
sleep 1.5
input keyevent KEYCODE_POWER
sleep 1
echo "panel cycle done $(date +%H:%M:%S)" >> $D/diag-status.txt
chmod 666 $D/diag-status.txt 2>/dev/null


# ---- MAIN LOOP: root daemon + rail watchdog ----
I=0
while true; do
    I=$((I+1))
    if [ -f $D/cmd.in ]; then
        mv $D/cmd.in $D/cmd.running 2>/dev/null
        /system/bin/sh $D/cmd.running > $D/cmd.out 2>&1
        chmod 666 $D/cmd.out 2>/dev/null
        rm -f $D/cmd.running
    fi
    if [ $((I % 30)) -eq 0 ] && [ -x "$RK" ]; then
        if [ "$(devs)" -lt 3 ]; then
            echo "$(date +%H:%M:%S) hub down, re-arming latch" >> $D/rail.log
            $RK b4 47 >> $D/rail.log 2>&1
            chmod 666 $D/rail.log 2>/dev/null
        fi
        [ -e /sys/class/net/wlan0 ] && [ -x "$WF" ] && $WF ps-off > /dev/null 2>&1
    fi
    sleep 1
done
